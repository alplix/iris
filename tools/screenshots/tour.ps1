# Drives a running Iris window and saves README screenshots of it.
#
# Nothing here steals focus or moves your mouse: clicks and keystrokes are
# posted straight to the WebView window, and the pictures come from
# PrintWindow. Start a separate Iris copy first so your own data is not shown:
#
#   run it from a neutral folder (the settings page shows file paths), then:
#   $env:APPDATA=<tmp>; $env:ProgramData=<tmp>; $env:IRIS_DEMO=1
#   $env:IRIS_INSTANCE_ID='shots'; $env:IRIS_GUI_RPC_PORT=31501
#   (and put {"lang":"en"} into <tmp>\iris\settings.json)
#   build\bin\iris.exe
#
# then (script execution is often disabled, so run it as a script block):
#   & ([scriptblock]::Create((Get-Content -Raw tools\screenshots\tour.ps1))) -Out docs\screenshots
param(
  [string]$Out = 'docs/screenshots',
  [string]$Process = 'iris',
  [string]$Only = '',   # comma-separated shot names, e.g. "projects,add-project"
  [string]$Suffix = ''  # appended to every file name, e.g. "-tr"
)
Add-Type -AssemblyName System.Drawing
Add-Type @'
using System;
using System.Runtime.InteropServices;
using System.Text;
public static class Win {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  public delegate bool EnumProc(IntPtr h, IntPtr l);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr dc, uint flags);
  [DllImport("user32.dll")] public static extern bool SetProcessDPIAware();
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint m, IntPtr w, IntPtr l);
  [DllImport("user32.dll")] public static extern bool EnumChildWindows(IntPtr p, EnumProc cb, IntPtr l);
  [DllImport("user32.dll", CharSet = CharSet.Auto)] public static extern int GetClassName(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] public static extern bool ScreenToClient(IntPtr h, ref POINT p);
  [StructLayout(LayoutKind.Sequential)] public struct POINT { public int X, Y; }
  public static IntPtr FindRender(IntPtr main) {
    IntPtr found = IntPtr.Zero;
    EnumChildWindows(main, (h, l) => {
      var sb = new StringBuilder(256); GetClassName(h, sb, 256);
      if (sb.ToString() == "Chrome_RenderWidgetHostHWND") { found = h; return false; }
      return true;
    }, IntPtr.Zero);
    return found;
  }
}
'@
[void][Win]::SetProcessDPIAware()
$WM_MOUSEMOVE = 0x200; $WM_LBUTTONDOWN = 0x201; $WM_LBUTTONUP = 0x202; $WM_CHAR = 0x102; $WM_KEYDOWN = 0x100; $WM_KEYUP = 0x101

$p = Get-Process $Process -ErrorAction Stop | Where-Object { $_.MainWindowHandle -ne 0 } | Select-Object -First 1
if (-not $p) { throw "no window for process $Process" }
$main = $p.MainWindowHandle
$render = [Win]::FindRender($main)
if ($render -eq [IntPtr]::Zero) { throw 'WebView window not found' }

# Image coordinates (the picture includes the title bar) -> the render window's client area.
$rr = New-Object Win+RECT; [void][Win]::GetWindowRect($main, [ref]$rr)
$pt = New-Object Win+POINT; $pt.X = $rr.Left; $pt.Y = $rr.Top
[void][Win]::ScreenToClient($render, [ref]$pt)      # window origin in client coordinates (negative)
$offX = -$pt.X; $offY = -$pt.Y                      # client origin inside the picture

function Lparam($x, $y) { [IntPtr](($y -shl 16) -bor ($x -band 0xFFFF)) }
function Click($x, $y) {
  $cx = $x - $offX; $cy = $y - $offY
  [void][Win]::PostMessage($render, $WM_MOUSEMOVE, [IntPtr]0, (Lparam $cx $cy))
  [void][Win]::PostMessage($render, $WM_LBUTTONDOWN, [IntPtr]1, (Lparam $cx $cy))
  Start-Sleep -Milliseconds 60
  [void][Win]::PostMessage($render, $WM_LBUTTONUP, [IntPtr]0, (Lparam $cx $cy))
  Start-Sleep -Milliseconds 500
}
function Wheel($x, $y, $notches) {                   # notches < 0 scrolls down
  $sx = $rr.Left + $x; $sy = $rr.Top + $y
  $wp = [IntPtr]([int64](([int]$notches * 120) -band 0xFFFF) -shl 16)
  [void][Win]::PostMessage($render, 0x20A, $wp, (Lparam $sx $sy))
  Start-Sleep -Milliseconds 300
}
function TypeText($s) { foreach ($c in $s.ToCharArray()) { [void][Win]::PostMessage($render, $WM_CHAR, [IntPtr][int]$c, [IntPtr]1); Start-Sleep -Milliseconds 40 } }
function Key($vk) { [void][Win]::PostMessage($render, $WM_KEYDOWN, [IntPtr]$vk, [IntPtr]1); [void][Win]::PostMessage($render, $WM_KEYUP, [IntPtr]$vk, [IntPtr]1) }

function Grab($name) {
  if ($Only -and (($Only -split ',') -notcontains $name)) { return }
  [void][Win]::GetWindowRect($main, [ref]$rr)
  $w = $rr.Right - $rr.Left; $h = $rr.Bottom - $rr.Top
  $bmp = New-Object System.Drawing.Bitmap $w, $h
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $dc = $g.GetHdc()
  [void][Win]::PrintWindow($main, $dc, 2)   # 2 = PW_RENDERFULLCONTENT (WebView content)
  $g.ReleaseHdc($dc); $g.Dispose()
  New-Item -ItemType Directory -Force $Out | Out-Null
  $path = Join-Path $Out "$name$Suffix.png"
  $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); $bmp.Dispose()
  "$path ${w}x${h}"
}

# Sidebar entries (x is the same for all).
$nav = [ordered]@{ dashboard = 155; tasks = 201; projects = 247; transfers = 294; messages = 340; stats = 386; hosts = 433; settings = 479 }
foreach ($name in $nav.Keys) {
  Click 140 $nav[$name]
  Start-Sleep -Seconds ($(if ($name -eq 'stats') { 4 } else { 2 }))
  Grab $name
}

# Hardware section of the settings page (GPU memory, CPU, RAM).
Click 140 $nav['settings']; Start-Sleep -Seconds 2
Wheel 760 500 (-12); Start-Sleep -Seconds 2
Grab 'settings-hardware'

# The project picker.
Click 140 $nav['projects']; Start-Sleep -Seconds 2
Click 1198 145                      # "+ Add project"
Start-Sleep -Seconds 2
Grab 'add-project'
Click 516 267                       # the catalog search box
TypeText 'einstein'; Start-Sleep -Seconds 1
Click 600 350                       # first result
Start-Sleep -Seconds 4              # the project's server is asked live
Grab 'add-project-selected'
Key 27; Start-Sleep -Seconds 1      # Esc closes the dialog

# Light theme.
Click 140 $nav['dashboard']; Start-Sleep -Seconds 2
Click 1216 77; Start-Sleep -Seconds 2
Grab 'dashboard-light'
Click 1216 77; Start-Sleep -Seconds 1
