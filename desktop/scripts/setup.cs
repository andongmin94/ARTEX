using System;
using System.Diagnostics;
using System.IO;
using System.Reflection;
using System.Text;
using System.Windows.Forms;

class Setup {
  [STAThread] static int Main(string[] args) {
    string temporary = Path.Combine(Path.GetTempPath(), "ARTEX-setup-" + Guid.NewGuid().ToString("N"));
    bool quiet = Array.IndexOf(args, "--quiet") >= 0;
    try {
      string root = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Programs", "ARTEX");
      bool register = true;
      foreach (string arg in args) { if (arg.StartsWith("--root=")) root = Path.GetFullPath(arg.Substring(7)); if (arg == "--no-registration") register = false; }
      if (!quiet && MessageBox.Show("ARTEX를 설치합니다. 기존 사용자 데이터는 유지됩니다.\n\n" + root, "ARTEX 설치", MessageBoxButtons.OKCancel, MessageBoxIcon.Information) != DialogResult.OK) return 0;
      Directory.CreateDirectory(temporary);
      Assembly assembly = Assembly.GetExecutingAssembly();
      foreach (string name in new string[] { "app.zip", "release.json", "trust.json", "install.ps1" }) {
        using (Stream source = assembly.GetManifestResourceStream(name)) using (FileStream destination = File.Create(Path.Combine(temporary, name))) source.CopyTo(destination);
      }
      string development = "";
#if DEVELOPMENT
      development = " -Development";
#endif
      string command = "-Mode Install -Root " + PinnedInstaller.Quote(root) + " -Archive " + PinnedInstaller.Quote(Path.Combine(temporary, "app.zip")) + " -Manifest " + PinnedInstaller.Quote(Path.Combine(temporary, "release.json")) + " -Trust " + PinnedInstaller.Quote(Path.Combine(temporary, "trust.json")) + development + (register ? "" : " -NoRegistration");
      string diagnostic;
      if (PinnedInstaller.Run(Path.Combine(temporary, "install.ps1"), command, out diagnostic) != 0) throw new Exception(diagnostic);
      if (!quiet) MessageBox.Show("설치가 완료되었습니다. 시작 메뉴에서 ARTEX를 실행하세요.", "ARTEX 설치", MessageBoxButtons.OK, MessageBoxIcon.Information);
      return 0;
    } catch (Exception error) { if (!quiet) MessageBox.Show(error.Message, "ARTEX 설치 실패", MessageBoxButtons.OK, MessageBoxIcon.Error); else Console.Error.WriteLine(error.Message); return 1; }
    finally { if (Directory.Exists(temporary)) Directory.Delete(temporary, true); }
  }
}
