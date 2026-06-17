# zcoms-team

The **team** component for [zcoms](https://github.com/Zouriel/zcoms): team
coordination, task delegation, GitHub Projects v2 sync, and automated standups,
driven from Telegram.

Pure-Go (SQLite via `modernc.org/sqlite`, no cgo). It persists to
`~/.config/zcoms/zc-team/team.db`, serves commands on `team.sock` (driven by
`zc team …` and the bridge), reaches Telegram via the core daemon's IPC, runs
standup interviews via the errands component, and talks to GitHub using the
terminal's authenticated `gh`.

Requires the **bridge** and **errands** components.

## Install
```sh
zc install team        # pulls in bridge + errands, downloads the prebuilt binary
zc team delegator create hems-dev MoHE-HEMS 1
zc team staff add hems-dev @ali ali-dev staff 2
zc team standup create hems-morning hems-dev @hems_team 09:00 Indian/Maldives
```

Status: Phase 1 (SQLite + delegator/standup/staff CRUD + audit). Task pool,
GitHub sync, standups, and reports land in later phases.
