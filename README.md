# bibicontrol

A control plane for The Bibites. Spin up sim processes, query live
simulations and save files, and mutate them through a DSL.

**Status: early.** Nowhere near complete — see the issues for what's
done and what's planned.

## Contributing

PRs welcome, but read the issue labels first:

- **easy** — safe to single-shot or vibe-code with minimal review. 
- **medium** — proceed with caution. Requires some effort; review changes, think about what the code is doing and test it.
- **difficult** — extreme caution. Blind vibe-coding will be rejected. Either impossible to vibe code, several subtle failure modes exist, or needs an excellent design. Probably stay away unless you are a confident professional developer

Every PR needs tests. The one exception is the DLL side, which can't be
meaningfully tested in isolation: verify the DLL returns correct info
over the network, but don't write a suite inside the DLL itself. Please create a branch and checkout

No BepInEx mods — they don't work on macOS/ARM.

## "An agent can write code, so why not raw vibe code?"

Past a few thousand lines, AI produces an unmaintainable mess. Several
of these issues have subtle failure modes an agent will miss entirely. 
As the code base expands, it will reach a point where AI is not very effective in terms of cost- think giant context windows if you want to do sweeping changes
