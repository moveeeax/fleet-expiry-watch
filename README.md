# fleet-expiry-watch

`fleetwatch` is a command-line expiry register for GB fleet operators. Give it a
list of registration marks and the handful of dates nobody publishes an API for,
and it tells you what has already lapsed, what is about to, and what nobody has
recorded at all.

It exists because a lapsed MOT or an out-of-date Driver CPC is a prohibition and
an OCRS hit, and the usual defence against that is a wall planner.

## What it does

- **Traffic-light register** of every dated obligation across vehicles and drivers.
- **Escalation ladder** at 60 / 30 / 14 / 7 / 1 days before expiry, then daily for
  as long as an item stays lapsed. Nothing is re-sent for an unchanged item.
- **Monday morning digest**: what has lapsed, then what expires inside the window.
- **Printable status page**: one self-contained HTML file, no external assets, safe
  to hand to an auditor.
- **CSV export** of everything, carrying the Open Government Licence attribution
  the DVSA/DVLA data requires.

Items it tracks:

| Vehicle | Driver |
| --- | --- |
| MOT (DVSA API) | Driver licence |
| Vehicle tax (DVLA API) | Driver CPC / Code 95 |
| Insurance | Digital tacho card |
| O-licence disc | Driver medical |
| Tacho calibration | |
| ADR certificate | |
| LOLER inspection | |

Everything except `fleetwatch fetch` runs entirely offline against CSV files.

## Install

Go 1.25 or newer.

```sh
go install github.com/moveeeax/fleet-expiry-watch/cmd/fleetwatch@latest
```

Or from a clone:

```sh
git clone https://github.com/moveeeax/fleet-expiry-watch.git
cd fleet-expiry-watch
make build      # -> bin/fleetwatch
make test
```

## Usage

The repository ships a sample fleet of eight lorries and four drivers, dated so
that every rung of the ladder is exercised on 1 July 2026. This command runs
as-is from a clone:

```sh
fleetwatch report \
  -vehicles examples/vehicles.csv \
  -drivers examples/drivers.csv \
  -operator "Pennine Haulage Ltd" \
  -today 2026-07-01
```

```
Pennine Haulage Ltd - expiry register as at 2026-07-01

STATUS   SUBJECT       ITEM                  EXPIRES       DAYS  STAGE    SOURCE
LAPSED   AB12 CDE      MOT                   2026-06-12    -19   lapsed   manual
LAPSED   S. Kaur       Driver CPC (Code 95)  2026-06-20    -11   lapsed   manual
LAPSED   GK15 ABC      Insurance             2026-06-30    -1    lapsed   manual
DUE      BD19 KLM      MOT                   2026-07-02    1     T-1      manual
DUE      HL22 DEF      LOLER inspection      2026-07-02    1     T-1      manual
DUE      CF67 NPR      MOT                   2026-07-08    7     T-7      manual
DUE      M. Oyelaran   Digital tacho card    2026-07-08    7     T-7      manual
DUE      AB12 CDE      Tacho calibration     2026-07-15    14    T-14     manual
DUE      GK15 ABC      MOT                   2026-07-15    14    T-14     manual
DUE      P. Novak      Driver medical        2026-07-15    14    T-14     manual
...
UNKNOWN  S. Kaur       Driver medical        not recorded  -     unknown  manual

3 lapsed, 16 due within 60 days, 39 ok, 14 with no date recorded

Contains public sector information licensed under the Open Government Licence v3.0.
```

Drop `-today` to evaluate against the real date. `make example` runs exactly the
command above.

### The Monday digest

```sh
fleetwatch digest \
  -vehicles examples/vehicles.csv \
  -drivers examples/drivers.csv \
  -operator "Pennine Haulage Ltd" \
  -today 2026-07-01 -window 30
```

```
Pennine Haulage Ltd - compliance digest, Wednesday 1 July 2026
============================================================

LAPSED - act today (3)
  AB12 CDE               MOT                    lapsed 19 days ago (2026-06-12)
  S. Kaur                Driver CPC (Code 95)   lapsed 11 days ago (2026-06-20)
  GK15 ABC               Insurance              lapsed 1 day ago (2026-06-30)

EXPIRING WITHIN 30 DAYS (11)
  BD19 KLM               MOT                    1 day left (2026-07-02)
  HL22 DEF               LOLER inspection       1 day left (2026-07-02)
  ...
```

### Only the items escalating today

`alerts` prints just the rows whose ladder fires on the given day — the input a
nightly mailer would act on.

```sh
fleetwatch alerts -vehicles examples/vehicles.csv -today 2026-07-01
```

### Printable status page and CSV export

```sh
fleetwatch report -vehicles examples/vehicles.csv -drivers examples/drivers.csv \
  -today 2026-07-01 -format html -out status.html

fleetwatch report -vehicles examples/vehicles.csv -drivers examples/drivers.csv \
  -today 2026-07-01 -format csv -out register.csv
```

### Exit codes for cron

```sh
fleetwatch report -vehicles vehicles.csv -fail-on-lapsed
```

`0` clean, `1` bad input or unreadable files, `3` at least one item has lapsed.

## Input files

Column order does not matter, header names do, and unknown columns are ignored so
an operator's own spreadsheet can be used almost unchanged. All dates are
`YYYY-MM-DD` — there is no way to tell `03/04/2026` apart from `04/03/2026`, so
the ambiguous forms are rejected rather than guessed at.

`vehicles.csv` — `plate` is required:

```csv
plate,fleet_number,make,model,mot_expiry,tax_due,insurance_expiry,olicence_expiry,tacho_calibration_due,adr_expiry,loler_due
AB12CDE,101,Volvo,FH16,2026-06-12,2026-08-01,2027-01-31,2027-03-31,2026-07-15,,
```

`drivers.csv` — `name` is required:

```csv
driver_id,name,licence_expiry,cpc_expiry,tacho_card_expiry,medical_expiry
D01,A. Whitcombe,2029-04-02,2026-07-31,2027-06-14,2026-08-12
```

`plates.csv` — a single column, enough to see a first result:

```csv
registration
AB12 CDE
BD19 KLM
```

A bad row is reported on stderr and skipped; one mistyped plate does not stop a
50-vehicle import. Registration marks are normalised (`ab12 cde` and `AB12CDE`
are the same lorry), and Northern Ireland marks are rejected with a message
rather than sent to APIs that do not serve them.

Blank cells are not treated as compliant. They are reported as `UNKNOWN`, because
an obligation nobody has filled in is exactly the one that bites.

## Pulling MOT and tax automatically

`fleetwatch fetch` fills in the two dates that are published, from the free DVSA
MOT History API and the DVLA Vehicle Enquiry Service. Both require an API key
that has to be applied for; the tool reads them from the environment and refuses
to run without them rather than inventing data.

```sh
cp config.example.env .env    # then replace every REPLACE_ME
set -a && . ./.env && set +a

fleetwatch fetch -plates examples/plates.csv -out vehicles.csv
```

The result is an ordinary `vehicles.csv` you can keep editing by hand: the pull
owns `mot_expiry` and `tax_due`, you own the rest. DVSA is treated as
authoritative for MOT; DVLA's copy is only used when the DVSA pull is
unavailable.

No key is ever read from a file inside the repo, written to a report, or included
in an error message.

## Attribution

MOT and vehicle tax data comes from DVSA and DVLA under the Open Government
Licence v3.0. Every export and status page carries the required line:

> Contains public sector information licensed under the Open Government Licence v3.0.

## Scope

Deliberately not here: telematics, walkaround/defect reporting, tachograph file
analysis, PMI planning, DVLA driver licence checking (that needs per-driver
consent and a separate access route), and anything outside Great Britain.

## Licence

Not yet chosen. Until one is added, no permission to use, copy or distribute this
code is granted.
