# Changelog

All notable changes to this project are documented in this file. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project follows [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- MIT licence.
- Dependabot configuration for GitHub Actions and Go modules.

### Changed

- Minimum Go version raised from 1.25 to 1.26.
- CI now tests against Go 1.26 and 1.27, using `actions/checkout` v7 and
  `actions/setup-go` v7.

## [0.1.0] - 2026-07-26

### Added

- Fleet data model and the 60 / 30 / 14 / 7 / 1-day expiry escalation ladder.
- CSV import of vehicles, drivers and bare plate lists, with registration
  mark normalisation and rejection of ambiguous dates.
- Register output as a table, Monday digest, CSV export and a self-contained
  HTML status page, all carrying the Open Government Licence attribution.
- DVSA MOT History and DVLA Vehicle Enquiry Service clients.
- `fleetwatch` CLI with `report`, `digest`, `alerts`, `fetch` and `version`
  commands.

[Unreleased]: https://github.com/moveeeax/fleet-expiry-watch/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/moveeeax/fleet-expiry-watch/releases/tag/v0.1.0
