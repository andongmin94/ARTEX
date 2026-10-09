using System;
using System.Diagnostics;
using System.IO;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text;

// 서명된 네이티브 실행 파일과 그 안의 스크립트 해시를 확인한 뒤 실행한다.
class InstallerProcessJob : IDisposable {
  [StructLayout(LayoutKind.Sequential)] struct Limits {
    public long processTime, jobTime; public uint flags; public UIntPtr minimumWorkingSet, maximumWorkingSet;
    public uint activeProcesses; public UIntPtr affinity; public uint priority, scheduling;
  }
  [StructLayout(LayoutKind.Sequential)] struct Counters { public ulong readOperations, writeOperations, otherOperations, readBytes, writeBytes, otherBytes; }
  [StructLayout(LayoutKind.Sequential)] struct ExtendedLimits { public Limits limits; public Counters io; public UIntPtr processMemory, jobMemory, peakProcessMemory, peakJobMemory; }
  [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)] static extern IntPtr CreateJobObject(IntPtr security, string name);
  [DllImport("kernel32.dll", SetLastError = true)] static extern bool SetInformationJobObject(IntPtr job, int kind, ref ExtendedLimits limits, uint size);
  [DllImport("kernel32.dll", SetLastError = true)] static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);
  [DllImport("kernel32.dll", SetLastError = true)] static extern bool CloseHandle(IntPtr handle);
  IntPtr handle;
  public InstallerProcessJob() {
    handle = CreateJobObject(IntPtr.Zero, null);
    if (handle == IntPtr.Zero) throw new Exception("설치 도우미의 프로세스 격리를 준비할 수 없습니다.");
    ExtendedLimits limits = new ExtendedLimits { limits = new Limits { flags = 0x2000 } };
    if (!SetInformationJobObject(handle, 9, ref limits, (uint)Marshal.SizeOf(typeof(ExtendedLimits)))) { Dispose(); throw new Exception("설치 도우미의 자손 종료 정책을 적용할 수 없습니다."); }
  }
  public void Assign(Process process) {
    if (!AssignProcessToJobObject(handle, process.Handle)) {
      process.Kill(); if (!process.WaitForExit(5000)) throw new Exception("설치 도우미 종료를 확인할 수 없습니다.");
      throw new Exception("설치 도우미의 프로세스 격리를 적용할 수 없습니다.");
    }
  }
  public void Dispose() { if (handle != IntPtr.Zero) { CloseHandle(handle); handle = IntPtr.Zero; } }
}
class PinnedInstaller {
  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)] struct TrustFile {
    public uint size; [MarshalAs(UnmanagedType.LPWStr)] public string path; public IntPtr handle, subject;
  }
  [StructLayout(LayoutKind.Sequential)] struct TrustData {
    public uint size; public IntPtr policy, sip; public uint ui, revocation, union;
    public IntPtr file; public uint action; public IntPtr state, url; public uint flags, context; public IntPtr signatureSettings;
  }
  [DllImport("wintrust.dll", ExactSpelling = true, SetLastError = true)] static extern int WinVerifyTrust(IntPtr window, [In] ref Guid action, ref TrustData data);
  static void VerifyPublisher(string executable, string thumbprint) {
    TrustFile file = new TrustFile { size = (uint)Marshal.SizeOf(typeof(TrustFile)), path = executable };
    IntPtr information = Marshal.AllocHGlobal(Marshal.SizeOf(typeof(TrustFile)));
    Marshal.StructureToPtr(file, information, false);
    TrustData data = new TrustData { size = (uint)Marshal.SizeOf(typeof(TrustData)), ui = 2, revocation = 1, union = 1, file = information, action = 1, flags = 0x80 };
    Guid policy = new Guid("00AAC56B-CD44-11d0-8CC2-00C04FC295EE");
    try {
      if (WinVerifyTrust(new IntPtr(-1), ref policy, ref data) != 0) throw new Exception("설치 도우미의 Windows 코드 서명을 확인할 수 없습니다.");
      using (X509Certificate2 signer = new X509Certificate2(X509Certificate.CreateFromSignedFile(executable))) {
        if (!String.Equals(signer.Thumbprint, thumbprint, StringComparison.OrdinalIgnoreCase)) throw new Exception("설치 도우미의 서명자가 고정된 배포 인증서와 다릅니다.");
      }
    } finally { data.action = 2; WinVerifyTrust(new IntPtr(-1), ref policy, ref data); Marshal.DestroyStructure(information, typeof(TrustFile)); Marshal.FreeHGlobal(information); }
  }
  public static string Quote(string value) {
    StringBuilder result = new StringBuilder("\""); int slashes = 0;
    foreach (char c in value) {
      if (c == '\\') { slashes++; continue; }
      if (c == '"') { result.Append('\\', slashes * 2 + 1); result.Append(c); }
      else { result.Append('\\', slashes); result.Append(c); }
      slashes = 0;
    }
    result.Append('\\', slashes * 2); return result.Append('"').ToString();
  }
  public static int Run(string script, string arguments, out string diagnostic) {
    string[] integrity;
    using (Stream stream = Assembly.GetExecutingAssembly().GetManifestResourceStream("installer-integrity.txt")) using (StreamReader reader = new StreamReader(stream, Encoding.ASCII)) integrity = reader.ReadToEnd().Split('\n');
    if (integrity.Length != 3 || integrity[0].Length != 64) throw new Exception("설치 도우미의 무결성 정보가 올바르지 않습니다.");
#if DEVELOPMENT
    if (integrity[2] != "development") throw new Exception("개발 설치 도우미 정보가 올바르지 않습니다.");
#else
    if (integrity[2] != "signed" || integrity[1].Length != 40) throw new Exception("서명된 설치 도우미 정보가 올바르지 않습니다.");
    VerifyPublisher(Assembly.GetExecutingAssembly().Location, integrity[1]);
#endif
    using (SHA256 hash = SHA256.Create()) using (Stream input = File.OpenRead(script)) {
      if (!String.Equals(BitConverter.ToString(hash.ComputeHash(input)).Replace("-", ""), integrity[0], StringComparison.OrdinalIgnoreCase)) throw new Exception("설치 스크립트가 고정된 SHA256과 다릅니다. 앱을 계속 실행합니다.");
    }
    ProcessStartInfo start = new ProcessStartInfo(Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.System), @"WindowsPowerShell\v1.0\powershell.exe"), "-NoProfile -NonInteractive -ExecutionPolicy Bypass -File " + Quote(script) + " " + arguments) { UseShellExecute = false, CreateNoWindow = true, RedirectStandardOutput = true, RedirectStandardError = true, StandardOutputEncoding = Encoding.UTF8, StandardErrorEncoding = Encoding.UTF8 };
    start.EnvironmentVariables.Remove("PSModulePath");
    StringBuilder output = new StringBuilder();
    using (InstallerProcessJob job = new InstallerProcessJob()) using (Process process = new Process { StartInfo = start }) {
      process.OutputDataReceived += delegate(object sender, DataReceivedEventArgs e) { if (e.Data != null) lock (output) output.AppendLine(e.Data); };
      process.ErrorDataReceived += delegate(object sender, DataReceivedEventArgs e) { if (e.Data != null) lock (output) output.AppendLine(e.Data); };
      process.Start(); job.Assign(process); process.BeginOutputReadLine(); process.BeginErrorReadLine(); process.WaitForExit(); diagnostic = output.ToString(); return process.ExitCode;
    }
  }
}
class InstallerHost {
  static int Main(string[] args) {
    string log = null;
    try {
      string script = null; StringBuilder arguments = new StringBuilder();
      foreach (string arg in args) {
        if (arg.StartsWith("--script=")) script = Path.GetFullPath(arg.Substring(9));
        else if (arg.StartsWith("--log=")) log = Path.GetFullPath(arg.Substring(6));
        else arguments.Append(PinnedInstaller.Quote(arg)).Append(' ');
      }
      if (script == null) throw new Exception("검증할 설치 스크립트가 지정되지 않았습니다.");
      if (Array.IndexOf(args, "Uninstall") >= 0 && String.Equals(Path.GetDirectoryName(Assembly.GetExecutingAssembly().Location), Path.GetDirectoryName(script), StringComparison.OrdinalIgnoreCase)) {
        string temporary = Path.Combine(Path.GetTempPath(), "ARTEX-remove-" + Guid.NewGuid().ToString("N")); Directory.CreateDirectory(temporary);
        string host = Path.Combine(temporary, "ARTEX-InstallHost.exe"); File.Copy(Assembly.GetExecutingAssembly().Location, host);
        StringBuilder forwarded = new StringBuilder(); foreach (string arg in args) forwarded.Append(PinnedInstaller.Quote(arg)).Append(' ');
        forwarded.Append(" -ParentPid ").Append(Process.GetCurrentProcess().Id);
        Process.Start(new ProcessStartInfo(host, forwarded.ToString()) { UseShellExecute = false, CreateNoWindow = true }); return 0;
      }
      string diagnostic; int result = PinnedInstaller.Run(script, arguments.ToString(), out diagnostic);
      if (log != null) File.WriteAllText(log, diagnostic, Encoding.UTF8);
      foreach (string line in diagnostic.Split('\n')) {
        if (!line.StartsWith("ARTEX_RESTART ")) continue;
        string[] fields = line.TrimEnd('\r').Split(' ');
        if (fields.Length != 3) throw new Exception("재시작 요청 정보가 올바르지 않습니다.");
        string executable = Encoding.UTF8.GetString(Convert.FromBase64String(fields[1]));
        string home = Encoding.UTF8.GetString(Convert.FromBase64String(fields[2]));
        Process.Start(new ProcessStartInfo(executable, home.Length == 0 ? "" : PinnedInstaller.Quote("--artex-home=" + home)) { UseShellExecute = false, CreateNoWindow = true });
      }
      return result;
    } catch (Exception error) { if (log != null) File.WriteAllText(log, error.Message, Encoding.UTF8); Console.Error.WriteLine(error.Message); return 1; }
  }
}
