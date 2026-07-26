// Command fleetwatch turns a list of GB registration marks and a handful of
// manually kept dates into an expiry register: a traffic-light table, a Monday
// morning digest, a printable status page and a CSV export.
//
// Everything except `fetch` runs entirely offline. `fetch` needs free DVSA and
// DVLA API keys and refuses to run without them rather than pretending.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/moveeeax/fleet-expiry-watch/internal/dvla"
	"github.com/moveeeax/fleet-expiry-watch/internal/dvsa"
	"github.com/moveeeax/fleet-expiry-watch/internal/expiry"
	"github.com/moveeeax/fleet-expiry-watch/internal/fleet"
	"github.com/moveeeax/fleet-expiry-watch/internal/register"
	"github.com/moveeeax/fleet-expiry-watch/internal/report"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `fleetwatch - expiry register for GB fleet operators

Usage:
  fleetwatch report  [flags]   traffic-light table, CSV or printable HTML
  fleetwatch digest  [flags]   Monday morning summary: lapsed, then due soon
  fleetwatch alerts  [flags]   only the items whose escalation ladder fires today
  fleetwatch fetch   [flags]   pull MOT and tax from DVSA/DVLA (needs API keys)

Common flags:
  -vehicles PATH   vehicles.csv (plate plus dated columns)
  -drivers PATH    drivers.csv (name plus dated columns)
  -plates PATH     one-column list of registration marks
  -operator NAME   licence holder name, used as the report heading
  -today DATE      evaluate as at this YYYY-MM-DD instead of the system date
  -out PATH        write to a file instead of stdout

Run "fleetwatch <command> -h" for the flags of one command.
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		var ec exitCode
		if errors.As(err, &ec) {
			os.Exit(int(ec))
		}
		fmt.Fprintln(os.Stderr, "fleetwatch:", err)
		os.Exit(1)
	}
}

// exitCode carries a non-error, non-zero exit status such as "lapsed items
// found", which cron and CI want to see as a failure.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitCode(2)
	}
	switch args[0] {
	case "report":
		return cmdReport(args[1:], stdout, stderr)
	case "digest":
		return cmdDigest(args[1:], stdout, stderr)
	case "alerts":
		return cmdAlerts(args[1:], stdout, stderr)
	case "fetch":
		return cmdFetch(args[1:], stdout, stderr)
	case "version", "-version", "--version":
		fmt.Fprintf(stdout, "fleetwatch %s\n", version)
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	}
	fmt.Fprint(stderr, usage)
	return fmt.Errorf("unknown command %q", args[0])
}

// inputs are the flags shared by every command that reads a register.
type inputs struct {
	vehicles string
	drivers  string
	plates   string
	operator string
	today    string
	out      string
}

func (in *inputs) bind(fs *flag.FlagSet) {
	fs.StringVar(&in.vehicles, "vehicles", "", "path to vehicles.csv")
	fs.StringVar(&in.drivers, "drivers", "", "path to drivers.csv")
	fs.StringVar(&in.plates, "plates", "", "path to a one-column list of registration marks")
	fs.StringVar(&in.operator, "operator", "", "operator name for the report heading")
	fs.StringVar(&in.today, "today", "", "evaluate as at this date (YYYY-MM-DD), default today")
	fs.StringVar(&in.out, "out", "", "write output to this file instead of stdout")
}

func (in *inputs) day() (time.Time, error) {
	if in.today == "" {
		return time.Now(), nil
	}
	d, err := time.Parse(register.DateLayout, in.today)
	if err != nil {
		return time.Time{}, fmt.Errorf("-today: %q is not a %s date", in.today, register.DateLayout)
	}
	return d, nil
}

// load reads every source that was given and reports the bad rows on stderr.
func (in *inputs) load(stderr io.Writer) (fleet.Register, error) {
	var res register.Result
	if in.vehicles == "" && in.plates == "" && in.drivers == "" {
		return fleet.Register{}, errors.New("nothing to read: give at least one of -vehicles, -drivers or -plates")
	}
	for _, src := range []struct {
		path string
		fn   func(string) (register.Result, error)
	}{
		{in.vehicles, register.LoadVehicles},
		{in.plates, register.LoadPlates},
		{in.drivers, register.LoadDrivers},
	} {
		if src.path == "" {
			continue
		}
		r, err := src.fn(src.path)
		if err != nil {
			return fleet.Register{}, err
		}
		res.Merge(r)
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(stderr, "warning:", w.Error())
	}
	if len(res.Register.Vehicles) == 0 && len(res.Register.Drivers) == 0 {
		return fleet.Register{}, errors.New("no vehicles or drivers were read from the input")
	}
	return res.Register, nil
}

func (in *inputs) writer(stdout io.Writer) (io.Writer, func() error, error) {
	if in.out == "" {
		return stdout, func() error { return nil }, nil
	}
	f, err := os.Create(in.out)
	if err != nil {
		return nil, nil, err
	}
	return f, f.Close, nil
}

func cmdReport(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	var in inputs
	in.bind(fs)
	format := fs.String("format", "table", "output format: table, csv or html")
	failOnLapsed := fs.Bool("fail-on-lapsed", false, "exit 3 if anything has already lapsed")
	if err := fs.Parse(args); err != nil {
		return err
	}

	reg, day, opt, err := prepare(&in, stderr)
	if err != nil {
		return err
	}
	assessed := expiry.AssessAll(reg.Items(), day)

	w, closeFn, err := in.writer(stdout)
	if err != nil {
		return err
	}
	defer closeFn()

	switch strings.ToLower(*format) {
	case "table":
		err = report.Table(w, assessed, opt)
	case "csv":
		err = report.CSV(w, assessed, opt)
	case "html":
		err = report.HTML(w, assessed, opt)
	default:
		return fmt.Errorf("-format: unknown format %q (want table, csv or html)", *format)
	}
	if err != nil {
		return err
	}
	if err := closeFn(); err != nil {
		return err
	}
	if *failOnLapsed && report.Tally(assessed).Lapsed > 0 {
		return exitCode(3)
	}
	return nil
}

func cmdDigest(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("digest", flag.ContinueOnError)
	var in inputs
	in.bind(fs)
	window := fs.Int("window", 30, "days ahead to include in the digest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *window < 1 {
		return fmt.Errorf("-window: must be at least 1 day, got %d", *window)
	}

	reg, day, opt, err := prepare(&in, stderr)
	if err != nil {
		return err
	}
	w, closeFn, err := in.writer(stdout)
	if err != nil {
		return err
	}
	defer closeFn()
	if err := report.Digest(w, expiry.AssessAll(reg.Items(), day), *window, opt); err != nil {
		return err
	}
	return closeFn()
}

func cmdAlerts(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("alerts", flag.ContinueOnError)
	var in inputs
	in.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	reg, day, opt, err := prepare(&in, stderr)
	if err != nil {
		return err
	}

	var due []expiry.Assessment
	for _, a := range expiry.AssessAll(reg.Items(), day) {
		// No alert state is persisted yet, so nothing has been notified before.
		if expiry.ShouldNotify(a, expiry.StageNone) {
			due = append(due, a)
		}
	}
	w, closeFn, err := in.writer(stdout)
	if err != nil {
		return err
	}
	defer closeFn()
	if len(due) == 0 {
		fmt.Fprintf(w, "%s: no escalation due on %s\n", opt.Name(), day.Format(register.DateLayout))
		return closeFn()
	}
	if err := report.Table(w, due, opt); err != nil {
		return err
	}
	return closeFn()
}

func cmdFetch(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	var in inputs
	in.bind(fs)
	timeout := fs.Duration("timeout", 2*time.Minute, "overall timeout for the whole pull")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if in.out == "" {
		return errors.New("-out is required: fetch writes an updated vehicles.csv")
	}

	reg, _, _, err := prepare(&in, stderr)
	if err != nil {
		return err
	}

	motClient, motErr := dvsa.New()
	taxClient, taxErr := dvla.New()
	if motErr != nil && taxErr != nil {
		return fmt.Errorf("no data source is configured:\n  %v\n  %v", motErr, taxErr)
	}
	if motErr != nil {
		fmt.Fprintln(stderr, "warning: skipping MOT pull:", motErr)
	}
	if taxErr != nil {
		fmt.Fprintln(stderr, "warning: skipping tax pull:", taxErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	for i := range reg.Vehicles {
		v := &reg.Vehicles[i]
		if motClient != nil {
			resp, err := motClient.Lookup(ctx, v.Plate)
			if err != nil {
				fmt.Fprintf(stderr, "warning: %s: MOT lookup: %v\n", v.Display, err)
			} else {
				register.SetItem(v, resp.Item(v.Plate, v.Display))
				if v.Make == "" {
					v.Make = resp.Make
				}
				if v.Model == "" {
					v.Model = resp.Model
				}
			}
		}
		if taxClient != nil {
			resp, err := taxClient.Lookup(ctx, v.Plate)
			if err != nil {
				fmt.Fprintf(stderr, "warning: %s: tax lookup: %v\n", v.Display, err)
				continue
			}
			for _, it := range resp.Items(v.Plate, v.Display) {
				// DVSA is authoritative for MOT; only take DVLA's copy if the
				// MOT pull was skipped or failed.
				if it.Kind == fleet.KindMOT && motClient != nil {
					continue
				}
				register.SetItem(v, it)
			}
			if v.Make == "" {
				v.Make = resp.Make
			}
		}
	}

	f, err := os.Create(in.out)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := register.WriteVehicles(f, reg); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %s (%d vehicles)\n%s\n", in.out, len(reg.Vehicles), report.OGLAttribution)
	return nil
}

// prepare does the work every command shares: parse the date, read the files.
func prepare(in *inputs, stderr io.Writer) (fleet.Register, time.Time, report.Options, error) {
	day, err := in.day()
	if err != nil {
		return fleet.Register{}, time.Time{}, report.Options{}, err
	}
	reg, err := in.load(stderr)
	if err != nil {
		return fleet.Register{}, time.Time{}, report.Options{}, err
	}
	return reg, day, report.Options{Operator: in.operator, Today: day}, nil
}
