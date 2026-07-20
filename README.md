# Forge Solo — Windows build

Native Windows installer for **Forge Solo** (solo-mine BCH2 + merge-mine 1175 at home).
Not Docker: a Go tray launcher orchestrates a bundled PostgreSQL, the BCH2 + 1175 nodes,
and the stratum + api services, and serves the dashboard on `127.0.0.1`.

## Layout
- `launcher/` — Go tray launcher/orchestrator (`main.go`, `boot.go`, `web.go`) + `forge-solo.ico`
- `web/` — the dashboard (mirror of the app's `web/dist`)
- `forge-solo.iss` — Inno Setup installer script
- `init-db.sql` — Postgres schema
- *(not tracked)* `bin/` — compiled exes + prebuilt node binaries; `pgsql/` — portable PostgreSQL

## External binaries (place in `bin/` before building the installer)
- `bitcoincashIId.exe` — BCH2 node (Windows release)
- `elevenseventyfived.exe` — 1175 node (Windows release)
- `stratum.exe`, `api.exe` — cross-compiled from the forge-solo app:
  `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags '-s -w' -o stratum.exe ./cmd/stratum` (and `./cmd/api`)
- `pgsql/` — a portable Windows PostgreSQL (EnterpriseDB zip), extracted at the repo root

## Build
```sh
# 1) exe icon resource (regenerate if the icon changes):
cd launcher && rsrc -ico forge-solo.ico -arch amd64 -o rsrc.syso && cd ..
# 2) launcher:
cd launcher && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui -s -w" -o ../bin/forge-solo.exe . && cd ..
# 3) installer (Inno Setup via Docker):
docker run --rm -v "$PWD":/work amake/innosetup forge-solo.iss
```

## Design notes
- **Dynamic ports** — internal loopback services (Postgres, both node RPCs, ZMQ, stratum-internal, api)
  pick free ports at startup (`pickPort`) so they never collide with other software or Windows
  reserved ranges. Fixed ports: miner `3333`, dashboard `3080`, BCH2 P2P `8333`.
- **Graceful shutdown** — the launcher stops nodes via RPC `stop` (flush chainstate) before exit,
  so a restart resumes instead of resyncing.
- **Installer** — one elevated step adds a Defender exclusion for `%APPDATA%\ForgeSolo` and firewall
  rules for the miner (3333) and BCH2 P2P (8333). Per-user install; no admin required for the copy.
- Config/secrets live under `%APPDATA%\ForgeSolo`; the payout address is stored in the DB (set via Settings).

## Not yet done (pre public release)
- Code signing (unsigned → SmartScreen warning)
- Test matrix: Windows 10, varied hardware/antivirus
