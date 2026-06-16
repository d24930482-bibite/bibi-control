using System;
using System.IO;
using System.Linq;
using Mono.Cecil;
using Mono.Cecil.Cil;

// Injects `BibiControl.HeadlessController.Bootstrap()` at the top of
// ManagementScripts.AppInitializer.Awake() and adds the assembly reference, so the
// game loads + runs the mod without recompiling its source. Idempotent.

internal static class Program
{
	private static int Main(string[] args)
	{
		if (args.Length < 3)
		{
			Console.Error.WriteLine("usage: bibipatch <in BibitesAssembly.dll> <BibiControl.dll> <out BibitesAssembly.dll>");
			return 2;
		}

		string inAsm = Path.GetFullPath(args[0]);
		string ctrlPath = Path.GetFullPath(args[1]);
		string outAsm = Path.GetFullPath(args[2]);
		string managedDir = Path.GetDirectoryName(inAsm);

		var resolver = new DefaultAssemblyResolver();
		resolver.AddSearchDirectory(managedDir);
		resolver.AddSearchDirectory(Path.GetDirectoryName(ctrlPath));

		using var ctrl = AssemblyDefinition.ReadAssembly(ctrlPath);
		var bootstrap = ctrl.MainModule
			.GetType("BibiControl.HeadlessController")?
			.Methods.FirstOrDefault(m => m.Name == "Bootstrap" && m.IsStatic && m.Parameters.Count == 0);
		if (bootstrap == null)
		{
			Console.Error.WriteLine("could not find public static BibiControl.HeadlessController.Bootstrap()");
			return 3;
		}

		using var asm = AssemblyDefinition.ReadAssembly(inAsm, new ReaderParameters { AssemblyResolver = resolver });
		var module = asm.MainModule;

		var appInit = module.GetType("ManagementScripts.AppInitializer");
		if (appInit == null)
		{
			Console.Error.WriteLine("ManagementScripts.AppInitializer not found in target assembly");
			return 4;
		}
		var awake = appInit.Methods.FirstOrDefault(m => m.Name == "Awake" && m.Parameters.Count == 0);
		if (awake == null || !awake.HasBody)
		{
			Console.Error.WriteLine("AppInitializer.Awake() not found / has no body");
			return 5;
		}

		bool already = awake.Body.Instructions.Any(i =>
			i.OpCode == OpCodes.Call && i.Operand is MethodReference mr &&
			mr.Name == "Bootstrap" && mr.DeclaringType.FullName == "BibiControl.HeadlessController");
		if (already)
		{
			Console.WriteLine("already patched; writing unchanged copy");
			asm.Write(outAsm);
			return 0;
		}

		var imported = module.ImportReference(bootstrap);
		var il = awake.Body.GetILProcessor();
		il.InsertBefore(awake.Body.Instructions[0], il.Create(OpCodes.Call, imported));

		asm.Write(outAsm);
		Console.WriteLine("patched OK: injected Bootstrap() into AppInitializer.Awake() -> " + outAsm);
		return 0;
	}
}
