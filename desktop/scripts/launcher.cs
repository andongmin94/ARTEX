using System;
using System.Diagnostics;
using System.IO;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text;
using System.Text.RegularExpressions;
using System.Threading;
using System.Windows.Forms;

class InstallRootLock : IDisposable {
  Mutex mutex; bool held;
  [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)] static extern uint GetLongPathName(string path, StringBuilder buffer, uint length);
  static string Canonical(string root) {
    string cursor = Path.GetFullPath(root).TrimEnd('\\'); List<string> tail = new List<string>();
    while (!Directory.Exists(cursor)) { tail.Insert(0, Path.GetFileName(cursor)); cursor = Path.GetDirectoryName(cursor); if (String.IsNullOrEmpty(cursor)) throw new Exception("설치 폴더의 실제 경로를 확인할 수 없습니다."); }
    StringBuilder buffer = new StringBuilder(32768);
    if (GetLongPathName(cursor, buffer, (uint)buffer.Capacity) == 0) throw new Exception("설치 폴더의 실제 경로를 확인할 수 없습니다.");
    string value = buffer.ToString(); foreach (string part in tail) value = Path.Combine(value, part);
    return value.TrimEnd('\\').ToUpperInvariant();
  }
  public InstallRootLock(string root) {
    string name;
    using (SHA256 hash = SHA256.Create()) name = BitConverter.ToString(hash.ComputeHash(Encoding.UTF8.GetBytes(Canonical(root)))).Replace("-", "").ToLowerInvariant();
    mutex = new Mutex(false, "Local\\ARTEX-Install-" + name);
    try { held = mutex.WaitOne(60000); } catch (AbandonedMutexException) { held = true; }
    if (!held) { mutex.Dispose(); throw new Exception("다른 설치 작업이 진행 중입니다. 작업이 끝난 뒤 다시 시도하세요."); }
  }
  public void Dispose() { if (held) { mutex.ReleaseMutex(); held = false; } mutex.Dispose(); }
}

class Launcher {
  static string Quote(string value) {
    StringBuilder result = new StringBuilder("\""); int slashes = 0;
    foreach (char c in value) {
      if (c == '\\') { slashes++; continue; }
      if (c == '"') { result.Append('\\', slashes * 2 + 1); result.Append(c); }
      else { result.Append('\\', slashes); result.Append(c); }
      slashes = 0;
    }
    result.Append('\\', slashes * 2); return result.Append('"').ToString();
  }
  static string ReadVersion(string file) {
    string value = File.ReadAllText(file).Trim();
    if (!Regex.IsMatch(value, @"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$")) throw new Exception("설치된 앱 버전이 올바르지 않습니다.");
    return value;
  }
  static void Pointer(string root, string value) {
    string temporary = Path.Combine(root, "current-" + Guid.NewGuid().ToString("N") + ".tmp");
    File.WriteAllText(temporary, value, Encoding.ASCII);
    File.Replace(temporary, Path.Combine(root, "current.txt"), null);
  }
  [STAThread] static int Main(string[] args) {
    try {
      string root = AppDomain.CurrentDomain.BaseDirectory;
      StringBuilder arguments = new StringBuilder(); foreach (string arg in args) arguments.Append(Quote(arg)).Append(' ');
      string current, previous = null;
      string pending = Path.Combine(root, "pending.txt");
      Process child;
      using (InstallRootLock rootLock = new InstallRootLock(root)) {
        current = ReadVersion(Path.Combine(root, "current.txt"));
        if (File.Exists(pending)) {
          string[] upgrade = File.ReadAllLines(pending);
          if (upgrade.Length != 2 || upgrade[1] != current) throw new Exception("적용 대기 중인 업데이트 정보가 올바르지 않습니다.");
          previous = upgrade[0];
          if (!Regex.IsMatch(previous, @"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$")) throw new Exception("복구할 앱 버전이 올바르지 않습니다.");
        }
        child = Process.Start(new ProcessStartInfo(Path.Combine(root, "versions", current, "ARTEX.exe"), arguments.ToString()) { UseShellExecute = false, WorkingDirectory = root });
      }
      if (previous == null) return 0;
      string healthy = Path.Combine(root, "healthy-" + current + ".txt");
      for (int i = 0; i < 450; i++) {
        if (File.Exists(healthy) && File.ReadAllText(healthy).Trim() == current) {
          using (InstallRootLock rootLock = new InstallRootLock(root)) {
            if (ReadVersion(Path.Combine(root, "current.txt")) == current && File.Exists(pending) && File.ReadAllLines(pending)[1] == current) { File.Delete(pending); File.Delete(healthy); }
          }
          return 0;
        }
        if (child.HasExited) break;
        Thread.Sleep(100);
      }
      if (!child.HasExited) {
        Process kill = Process.Start(new ProcessStartInfo(Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.System), "taskkill.exe"), "/PID " + child.Id + " /T /F") { UseShellExecute = false, CreateNoWindow = true });
        kill.WaitForExit(10000); child.WaitForExit(10000);
        if (!child.HasExited) throw new Exception("새 앱을 안전하게 종료할 수 없습니다. 기존 버전을 유지했습니다.");
      }
      using (InstallRootLock rootLock = new InstallRootLock(root)) {
        if (ReadVersion(Path.Combine(root, "current.txt")) != current || !File.Exists(pending)) return 1;
        string ownerFile = Path.Combine(root, "versions", current, ".artex-version-owner.txt");
        string[] owner = File.ReadAllLines(ownerFile);
        if (owner.Length != 4 || owner[0] != "ARTEX/1" || owner[2] != current || !Regex.IsMatch(owner[3], "^[a-f0-9]{64}$")) throw new Exception("실패한 앱 버전의 설치 소유권을 확인할 수 없습니다.");
        File.WriteAllText(Path.Combine(root, "versions", current, ".artex-ready-failed.txt"), owner[3] + "\n" + current, new UTF8Encoding(false));
        Pointer(root, previous); File.Delete(pending);
        Process.Start(new ProcessStartInfo(Path.Combine(root, "versions", previous, "ARTEX.exe"), arguments.ToString()) { UseShellExecute = false, WorkingDirectory = root });
        File.WriteAllText(Path.Combine(root, "update-failure.txt"), "새 앱의 준비 확인에 실패해 이전 앱 버전으로 복구했습니다.", Encoding.UTF8);
      }
      return 1;
    } catch (Exception error) { MessageBox.Show(error.Message, "ARTEX", MessageBoxButtons.OK, MessageBoxIcon.Error); return 1; }
  }
}
