# BibiControl — headless + IPC mod for The Bibites

A small, additive mod baked into the game's own assembly (`BibitesAssembly`). It
adds:

- **Headless launch**: a `-bibiteHeadless` flag that boots straight into the
  simulation with a chosen save, skipping interactive menus.
- **An IPC server**: a TCP server speaking the same newline-delimited-JSON
  envelope protocol as the Go `ipc` package, implementing `STOP`, `RESUME`,
  `INFO`, and `RELOAD`.

No BepInEx / Doorstop. The bootstrap runs via Unity's
`[RuntimeInitializeOnLoadMethod]`, so it works on every platform the managed
assembly runs on (including macOS/ARM).

## Files

| File | Purpose |
|------|---------|
| `BibiControl/HeadlessController.cs` | Arg parsing, server bootstrap, headless redirect |
| `BibiControl/IpcServer.cs` | TCP server, envelope framing, main-thread dispatch, command registry |
| `BibiControl/SimCommands.cs` | `STOP` / `RESUME` / `INFO` / `RELOAD` handlers |
| `BibiControl/Envelope.cs` | JSON envelope DTO (mirrors `ipc.Envelope`) |

## Build (bake into `BibitesAssembly`)

The Bibites ships as a Unity Mono assembly with a recompilable, decompiled
source tree and a `BibitesAssembly.csproj` (SDK-style, globs all `*.cs`).

1. Copy the `BibiControl/` folder into the **root of the decompiled source tree**
   (the directory that contains `BibitesAssembly.csproj`). SDK-style globbing
   picks the new files up automatically — no `.csproj` edit needed for sources.
2. Ensure `System.dll` is referenced (needed for `System.Net.Sockets`). With
   `UseWindowsForms=true` it usually already is; if the build can't find
   `TcpListener`, add to the `csproj`:
   ```xml
   <Reference Include="System" />
   ```
3. Build against the game's managed assemblies. The simplest path is to point the
   reference assemblies at the game install's `Managed` folder (Rider/VS resolve
   the `<Reference Include="UnityEngine.*" />` items from there), then:
   ```
   dotnet build BibitesAssembly.csproj -c Release
   ```
4. Copy the resulting `BibitesAssembly.dll` over the one in
   `TheBibites_Data/Managed/` (back up the original first).

> The mod only calls existing **public** game APIs plus UnityEngine and the
> game's bundled Newtonsoft.Json, so it compiles as part of the assembly with no
> new dependencies.

## Run

Launch the game with the headless flag and a save, combined with Unity's own
headless switches:

```
TheBibites.exe -batchmode -nographics \
  -bibiteHeadless \
  -bibiteSave latest \
  -bibiteIpcPort 43100
```

Flags:

| Flag | Default | Meaning |
|------|---------|---------|
| `-bibiteHeadless` | off | Enable headless mode + start the IPC server |
| `-bibiteSave <path\|latest>` | (none) | Save to auto-load; `latest` = newest save/autosave |
| `-bibiteIpcPort <port>` | `43100` | TCP listen port |
| `-bibiteIpcHost <host>` | `127.0.0.1` | Bind host (`0.0.0.0` to listen on all interfaces) |

Without `-bibiteSave`, the server still starts but no world is loaded until a
`RELOAD` is sent.

## Drive it from Go

The control plane dials in (it is the TCP client) and uses the typed client in
`simctl`:

```go
rt, _ := noderuntime.Start(ctx, noderuntime.Spec{
    Process: ipc.ProcessSpec{
        Path: bibitesExe,
        Args: []string{"-batchmode", "-nographics",
            "-bibiteHeadless", "-bibiteSave", "latest", "-bibiteIpcPort", "43100"},
    },
    CompatAddr:     "127.0.0.1:43100",
    ConnectOnStart: true,
})

sim := simctl.New(rt)
prev, _ := sim.Stop(ctx)          // pause; prev.PreviousTimeScale is the old speed
info, _ := sim.Info(ctx)          // info.TPS / RealTPS / Paused / LastAutosave
_, _ = sim.Resume(ctx, prev.PreviousTimeScale) // unpause at the prior speed
_, _ = sim.Reload(ctx)            // reload most recent save
```

## Extending with a new command

1. **Go**: add a `Command*` constant and payload/result types in
   `ipc/commands.go`, plus a method on `simctl.Client`.
2. **DLL**: write a handler `object MyCmd(JToken payload)` in `SimCommands.cs`
   and `server.Register("MYCMD", MyCmd)`.

Handlers run on the Unity main thread, so they may read/write game state
directly. Keep the JSON field names identical on both sides.

## Notes / limits

- Headless mode briefly loads the menu scene, then redirects into the
  simulation. The default redirect needs no game-source changes. If the menu
  scene misbehaves under `-batchmode -nographics`, skip it entirely by inserting
  this in `ManagementScripts/AppInitializer.cs` `Start()`, before
  `GameManager.OpenMenu();`:
  ```csharp
  if (BibiControl.HeadlessController.Enabled)
  {
      string headlessSave = BibiControl.HeadlessController.ResolveSave();
      if (!string.IsNullOrEmpty(headlessSave))
      {
          GameManager.StartGame(headlessSave);
          return;
      }
  }
  ```
  This composes safely with the automatic redirect (whichever launches first
  wins; the other is a no-op).
- `STOP` reports the **target** time scale as `previous_time_scale`. It pauses by
  forcing the engine time scale to 0; `RESUME` sets both target and engine scale
  so the requested speed takes effect immediately.
- The IPC server cannot be unit-tested inside the DLL; verify behaviour over the
  network against a running build. The wire contract is exercised by
  `simctl/simctl_test.go`, which doubles as the contract spec.
