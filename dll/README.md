# BibiControl — headless + IPC mod for The Bibites

Adds two things to The Bibites:

- **Headless launch** — a `-bibiteHeadless` flag that boots straight into the
  simulation with a chosen save, with no menus and (combined with Unity's
  `-batchmode -nographics`) no window.
- **An IPC server** — a TCP server speaking the same newline-delimited-JSON
  envelope protocol as the Go `ipc` package, implementing `STOP`, `RESUME`,
  `INFO`, and `RELOAD`. The control plane (`simctl`) dials in and drives it.

No BepInEx / Doorstop. It is pure managed C#, so it runs on every platform the
game's managed assembly runs on (incl. macOS/ARM).

---

## Status (what is and isn't proven)

Verified on this machine (The Bibites, Unity **6000.0.44f1**, Mono):

| Piece | Status |
|-------|--------|
| Go control client (`simctl`) | ✅ built + unit-tested (`go test -race`) |
| Mod C# code | ✅ compiles cleanly against the real game assemblies |
| Headless launch | ✅ game runs windowless under `-batchmode -nographics`; custom flags reach the game |
| Mod actually running in-game (server up, commands answered) | ❌ **not yet** — see "Loading" below |

The blocker is purely **how the mod gets loaded**, not the mod logic. On this
Unity 6 build, dropping `BibiControl.dll` into `Managed` and listing it in
`ScriptingAssemblies.json` does **not** cause Unity to run the mod's
`[RuntimeInitializeOnLoadMethod]` startup hook (Unity never loads an assembly
nothing references). The mod must be part of an assembly the game already loads
— i.e. **baked into `BibitesAssembly`** (or called from it). See below.

---

## Files

| File | Purpose |
|------|---------|
| `BibiControl/HeadlessController.cs` | Arg parsing, server bootstrap (`[RuntimeInitializeOnLoadMethod]`), headless redirect |
| `BibiControl/IpcServer.cs` | TCP server, envelope framing, main-thread dispatch, command registry |
| `BibiControl/SimCommands.cs` | `STOP` / `RESUME` / `INFO` / `RELOAD` handlers |
| `BibiControl/Envelope.cs` | JSON envelope DTO (mirrors `ipc.Envelope`) |
| `BibiControl.csproj` | Companion-DLL build (handy for compile-checking against your install) |

---

## Building

### Compile-check against your game (easy, no install)

`BibiControl.csproj` builds the four files as a standalone DLL referencing the
shipped game assemblies. This proves the code matches your game's API:

```
dotnet build dll/BibiControl.csproj -c Release `
  -p:GameManaged="C:\Program Files (x86)\Steam\steamapps\common\The Bibites\The Bibites_Data\Managed"
```

(Default `GameManaged` already points at the typical Steam path.) This produces
`BibiControl.dll`, but on Unity 6 that standalone DLL will **not auto-load** (see
Status). It's a build/lint check, not the deployable artifact.

### Bake into `BibitesAssembly` (the deployable path)

This is the only reliable way to get the mod to run on Unity 6.

1. Get the game's decompiled, recompilable source + its `BibitesAssembly.csproj`.
2. Copy `dll/BibiControl/` into the source root (the SDK project globs `*.cs`).
3. Point the project's references at your install's `Managed` folder (e.g. a
   `Directory.Build.props` that adds `<AssemblySearchPaths>` for `Managed`, plus
   a .NET Framework reference pack such as
   `Microsoft.NETFramework.ReferenceAssemblies.net40`).
4. Build with a C# compiler that matches the source's language level
   (`BibitesAssembly.csproj` sets `LangVersion 14.0`). Decompiled IL can need a
   recent Roslyn and occasionally a small artifact fix.
5. Copy the resulting `BibitesAssembly.dll` over the one in
   `The Bibites_Data/Managed/` (back up the original first).

> Heads-up: recompiling the full ~94k-line decompiled assembly is the hard part.
> On this machine it did not build out of the box (compiler errors around `ref`
> expressions in the decompiled code, unrelated to this mod). A lighter
> alternative — IL-patching the shipped `BibitesAssembly.dll` to call
> `BibiControl.HeadlessController.Bootstrap()` (e.g. with Mono.Cecil), so the mod
> loads without recompiling the source — is feasible but not yet implemented here.

---

## Running (Steam gotchas matter)

Launching The Bibites' `.exe` directly makes Steam relaunch it through Steam and
**strip your command-line args** (only `ARG 0` reaches the game). Two fixes:

- **Add `steam_appid.txt`** containing `2736860` next to `The Bibites.exe`. Then a
  direct launch keeps your args. (Confirmed working.)
- **or** set the args as **Steam launch options** and start it from Steam.

Then launch headless (note: **quote save paths that contain spaces** — the Bibites
save folder usually does):

```
"…\The Bibites.exe" -batchmode -nographics ^
  -bibiteHeadless ^
  -bibiteSave "C:\Users\<you>\AppData\LocalLow\The Bibites\The Bibites\Savefiles\<world>\saves\_00.zip" ^
  -bibiteIpcPort 43100
```

Confirm it loaded by checking `Player.log`
(`…\AppData\LocalLow\The Bibites\The Bibites\Player.log`, or pass `-logFile <path>`)
for the line:

```
[BibiControl] mod loaded (headless=True)
```

Flags:

| Flag | Default | Meaning |
|------|---------|---------|
| `-bibiteHeadless` | off | Enable headless mode + start the IPC server |
| `-bibiteSave <path\|latest>` | (none) | Save to auto-load; `latest` = newest save the game finds (quote if it has spaces) |
| `-bibiteIpcPort <port>` | `43100` | TCP listen port |
| `-bibiteIpcHost <host>` | `127.0.0.1` | Bind host (`0.0.0.0` to listen on all interfaces) |

---

## Testing it (end to end)

1. Build + install the mod (bake-in path above) and add `steam_appid.txt`.
2. Launch headless with a quoted save path on port 43100.
3. From the repo, run the manual client and watch it exercise the commands:
   ```
   go run ./cmd/livetest 127.0.0.1:43100
   ```
   It waits for the world to load, then prints `INFO`, `STOP`, `INFO`,
   `RESUME x5`, `INFO`, `RELOAD`, `INFO`. (For the automated protocol test that
   needs no game, run `go test ./simctl/...`.)

---

## Driving it from Go

The control plane is the TCP client and uses `simctl`:

```go
sess, _ := ipc.Dial(ctx, "127.0.0.1:43100", nil)
sim := simctl.New(sess)

prev, _ := sim.Stop(ctx)                       // pause; prev.PreviousTimeScale = old speed
info, _ := sim.Info(ctx)                        // info.TPS / RealTPS / Paused / LastAutosave
_, _    = sim.Resume(ctx, prev.PreviousTimeScale) // unpause at the prior speed
_, _    = sim.Reload(ctx)                       // reload most recent save
```

`*ipc.Session`, `*ipc.OpaqueNode`, and `*noderuntime.Runtime` all satisfy
`simctl.Requester`, so this also composes with the process launcher in
`noderuntime`.

---

## Extending with a new command

1. **Go**: add a `Command*` constant + payload/result types in `ipc/commands.go`,
   and a method on `simctl.Client`.
2. **DLL**: write a handler `object MyCmd(JToken payload)` in `SimCommands.cs`
   and `server.Register("MYCMD", MyCmd)`.
3. **Test**: extend `simctl/simctl_test.go`'s fake server to cover it.

Handlers run on the Unity main thread, so they may read/write game state
directly. Keep the JSON field names identical on both sides.
