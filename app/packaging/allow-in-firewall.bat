net session >nul 2>&1
if %errorlevel% neq 0 (
  powershell -NoProfile -Command "Start-Process '%~f0' -Verb RunAs"
  exit /b
)
set "EXE=%~dp0tandem.exe"
netsh advfirewall firewall delete rule name=Tandem >nul 2>&1
netsh advfirewall firewall add rule name=Tandem dir=in action=allow program="%EXE%" enable=yes profile=any
echo.
echo Tandem is allowed inbound. Close this window.
pause
