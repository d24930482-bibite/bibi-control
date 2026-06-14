# The Bibites Smoke Debug Handoff

This is a small handoff for debugging game-load failures from the installed SQL
ref smoke saves.

## Current Situation

The all-observed SQL ref smoke test writes parser-valid zips, but game loading
has failed in Unity. Treat these as game-domain invariant failures until proven
otherwise.

Known first failure:

- Unity stack: `SettingScripts.MatterMaterialSettings.LoadState`
- Cause found: the smoke flipped `ArmorSettings.decay` from `false` to `true`
  without adding the companion decay fields the game expects.
- Current mitigation: `settings_material_values.bool_value` now chooses a
  material row where `decay=true`, so the smoke mutates it to `false`.

Second failure:

- Unity stack: `SettingScripts.ZoneSettings.LoadState`
- Cause found: the smoke changed enum-backed zone strings to fabricated
  values, first `zones[0].distribution = "CentricGradual_sql"`, and the
  generated zip also had `zones[0].movement = "None_sql"`.
- Current mitigation: `settings_zones.distribution` now falls back to a known
  `SpawnDistribution` member when no alternate distribution is observed, and
  `settings_zone_values.string_value` for the `movement` setting now chooses a
  known `MovementType` member.

Third failure:

- Unity stack: `ManagementScripts.WorldObjectsSpawner.SpawnPelletOfMatter` via
  `ManagementScripts.SaveSystem.LoadPellet`.
- Cause found: `pellets.material` used the settings material key
  `ArmorSettings`, but pellet and stomach content runtime material fields use
  names such as `Plant` and `Meat`.
- Current mitigation: material mutations for `pellets` and
  `bibite_stomach_contents` now choose an alternate observed runtime matter
  material instead of settings material keys.

## Where To Check First

Unity logs:

```text
~/.config/unity3d/The Bibites/The Bibites/Player.log
~/.config/unity3d/The Bibites/The Bibites/Player-prev.log
```

Search for:

```text
Exception
NullReferenceException
IndexOutOfRangeException
KeyNotFoundException
LoadState
LoadGame
```

Installed smoke saves:

```text
~/.config/unity3d/The Bibites/The Bibites/Savefiles/all-observed-sqlref-autosave_20260301021357.zip
~/.config/unity3d/The Bibites/The Bibites/Savefiles/all-observed-sqlref-dasdasd.zip
```

Generated smoke saves:

```text
/tmp/bibicontrol-smoke/all-observed-sqlref-autosave_20260301021357.zip
/tmp/bibicontrol-smoke/all-observed-sqlref-dasdasd.zip
```

Source fixtures:

```text
testdata/saves/the-bibites/autosave_20260301021357.zip
testdata/saves/the-bibites/dasdasd.zip
```

## Code Pointers

Main installed smoke test:

```text
savemutator/thebibites/sqlref_test.go
TestSmokeLiveSQLRefMatrixInstallsAllObservedFields
```

Fixture selection:

```text
selectLiveSQLRefSmokeFixtures
```

Rows and next values chosen for mutation:

```text
liveSQLRefMutationCases
addLive*Cases
nextLiveSQLRefValue
firstLiveSettingValueRow
```

Commit, reparse, and install path:

```text
commitLiveSQLRefFixture
installLiveSQLRefSmokeSave
```

Resolver allowlist:

```text
savemutator/thebibites/sqlref.go
```

## Useful Commands

Reinstall the all-observed smoke directly into Unity Savefiles:

```bash
BIBITES_SAVEFILES_DIR="$HOME/.config/unity3d/The Bibites/The Bibites/Savefiles" GOMODCACHE=/tmp/bibicontrol-go-mod GOCACHE=/tmp/bibicontrol-go-build go test ./savemutator/thebibites -run TestSmokeLiveSQLRefMatrixInstallsAllObservedFields -count=1 -v
```

Inspect settings from a generated smoke zip:

```bash
unzip -p /tmp/bibicontrol-smoke/all-observed-sqlref-autosave_20260301021357.zip settings.bb8settings | jq '.materials'
```

Compare a specific entry between source and smoke:

```bash
unzip -p testdata/saves/the-bibites/autosave_20260301021357.zip settings.bb8settings | jq '.'
unzip -p /tmp/bibicontrol-smoke/all-observed-sqlref-autosave_20260301021357.zip settings.bb8settings | jq '.'
```

## Debug Strategy

1. Read the latest Unity stack trace first. It usually names the loader class
   that rejected the save.
2. Map that loader class to the zip entry and normalized table being mutated.
3. Inspect the mutated JSON for that entry with `unzip -p ... | jq`.
4. If the value is parser-valid but game-invalid, keep the resolver test and
   live commit/reparse coverage, but make the installed smoke choose a
   load-safer row or value. Known enum-backed zone strings should use another
   game enum member, not a synthetic `_sql` suffix. Runtime matter material
   fields should use observed runtime names, not `settings.materials` keys.
5. Reinstall the smoke into the Unity Savefiles directory and retry in-game.

The installed smoke is intentionally broad. It proves many writable refs at
once, but it can easily violate game invariants that the parser cannot know.
