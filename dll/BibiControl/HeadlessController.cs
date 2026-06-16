using System;
using ManagementScripts;
using UnityEngine;
using UnityEngine.SceneManagement;

namespace BibiControl;

// Headless-mode entry point and IPC bootstrap.
//
// Because this is compiled into BibitesAssembly, Unity invokes Bootstrap()
// automatically at startup via [RuntimeInitializeOnLoadMethod] — no external
// loader (BepInEx/Doorstop) is required, so it works on every platform the
// managed assembly runs on (incl. macOS/ARM).
//
// Command-line flags (pass after the executable; combine with Unity's own
// -batchmode -nographics for a true headless run):
//   -bibiteHeadless            enable headless mode and start the IPC server
//   -bibiteSave <path|latest>  save to auto-load on start ("latest" = newest)
//   -bibiteIpcPort <port>      TCP listen port for the IPC server (default 43100)
//   -bibiteIpcHost <host>      bind host (default 127.0.0.1; use 0.0.0.0 for any)
//
// Without -bibiteSave the server still starts but no world is auto-loaded; a
// RELOAD command can load the most recent save later.
public static class HeadlessController
{
	public static bool Enabled { get; private set; }
	public static string SaveArg { get; private set; }
	public static int Port { get; private set; } = 43100;
	public static string Host { get; private set; } = "127.0.0.1";

	private static bool _parsed;
	private static bool _launched;
	private static IpcServer _server;

	[RuntimeInitializeOnLoadMethod(RuntimeInitializeLoadType.BeforeSceneLoad)]
	private static void Bootstrap()
	{
		ParseArgs();
		if (!Enabled)
			return;
		StartServer();
		// AppInitializer.Start() opens the menu. Once the menu scene has loaded,
		// redirect into the simulation with the requested save instead of idling
		// in the menu. Gating on activeScene == Menu (rather than a scene name)
		// keeps this robust regardless of the bootstrap scene's name.
		SceneManager.sceneLoaded += OnSceneLoaded;
	}

	public static void ParseArgs()
	{
		if (_parsed)
			return;
		_parsed = true;

		string[] args = Environment.GetCommandLineArgs();
		for (int i = 0; i < args.Length; i++)
		{
			switch (args[i])
			{
				case "-bibiteHeadless":
					Enabled = true;
					break;
				case "-bibiteSave":
					if (i + 1 < args.Length)
						SaveArg = args[++i];
					break;
				case "-bibiteIpcPort":
					if (i + 1 < args.Length && int.TryParse(args[i + 1], out int port))
					{
						Port = port;
						i++;
					}
					break;
				case "-bibiteIpcHost":
					if (i + 1 < args.Length)
						Host = args[++i];
					break;
			}
		}
	}

	private static void StartServer()
	{
		if (_server != null)
			return;
		GameObject go = new GameObject("BibiControlIpcServer");
		UnityEngine.Object.DontDestroyOnLoad(go);
		_server = go.AddComponent<IpcServer>();
		_server.Configure(Host, Port);
		SimCommands.Register(_server);
		Debug.Log("[BibiControl] headless enabled; IPC server " + Host + ":" + Port);
	}

	private static void OnSceneLoaded(Scene scene, LoadSceneMode mode)
	{
		if (!Enabled || _launched)
			return;
		// Only act once the game has navigated to the menu (post-initialization).
		if (GameManager.activeScene != BibiteScenes.Menu)
			return;
		_launched = true;

		string save = ResolveSave();
		if (string.IsNullOrEmpty(save))
		{
			Debug.LogWarning("[BibiControl] headless: no save to load (use -bibiteSave or RELOAD).");
			return;
		}
		Debug.Log("[BibiControl] headless: loading save " + save);
		GameManager.StartGame(save);
	}

	// Resolves -bibiteSave into a concrete path. "latest" picks the newest save
	// (manual or autosave); returns null when nothing is available.
	public static string ResolveSave()
	{
		if (string.IsNullOrEmpty(SaveArg))
			return null;
		if (string.Equals(SaveArg, "latest", StringComparison.OrdinalIgnoreCase))
		{
			try { return SaveController.AnySave() ? SaveController.GetLastSave() : null; }
			catch { return null; }
		}
		return SaveArg;
	}
}
