This is a bibite control plane. Spin up processes, query sims + saves, mutate them with a DSL. Nowehere near complete. Check issues for progress. 




Open to PRs but read the labels please; easy means could be vibe coded or single shotted, medium means be cautious, difficult means exercise extreme caution; blind vibe coding is a no go. PRs will not be accepted without test suite, excluding things on DLL side as there is no real way to test that. Eg: ensure the dll returns correct info over network, but no need to write a suite inside the acutal dll

No bepinex mods. They do not work on macos/ARM

But "x agent" can code anything, so why do this?:
At code base sizes larger than a few k lines, AI will produce an unmaintainable mess. Some of these issues have subtle failure modes that an AI will miss completely



