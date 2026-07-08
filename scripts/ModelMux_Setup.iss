; ModelMux v3.0 Inno Setup 安装脚本
; 编译: iscc ModelMux_Setup.iss
; 输出: Output\ModelMux-v3.0-Setup.exe

#define MyAppName "ModelMux"
#define MyAppVersion "3.0.0"
#define MyAppPublisher "ModelMux Federation"
#define MyAppURL "https://github.com/lisiyu/modelmux"
#define MyAppExeName "modelmux.exe"

[Setup]
AppId={{A1B2C3D4-E5F6-7890-ABCD-EF1234567890}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppName}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}
AllowNoIcons=yes
LicenseFile=
OutputDir=Output
OutputBaseFilename=ModelMux-v{#MyAppVersion}-Setup
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64
MinVersion=10.0
DisableProgramGroupPage=yes
UninstallDisplayIcon={app}\modelmux.exe
SetupLogging=yes

[Languages]
Name: "chinesesimplified"; MessagesFile: "compiler:Languages\ChineseSimplified.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Files]
; ModelMux 主程序
Source: "modelmux.exe"; DestDir: "{app}"; Flags: ignoreversion
; NSSM 服务管理器
Source: "nssm.exe"; DestDir: "{app}"; Flags: ignoreversion
; Cloudflare Tunnel (可选)
Source: "cloudflared.exe"; DestDir: "{app}"; Flags: ignoreversion
; 默认配置文件
Source: "default-config.json"; DestDir: "{commonappdata}\ModelMux"; Flags: onlyifdoesntexist

[Dirs]
Name: "{commonappdata}\ModelMux"

[Icons]
Name: "{group}\ModelMux 管理面板"; Filename: "http://localhost:8000"
Name: "{group}\启动服务"; Filename: "net"; Parameters: "start ModelMux"
Name: "{group}\停止服务"; Filename: "net"; Parameters: "stop ModelMux"
Name: "{group}\{cm:UninstallProgram,{#MyAppName}}"; Filename: "{uninstallexe}"

[Run]
; 注册服务
Filename: "{app}\nssm.exe"; Parameters: "install ModelMux ""{app}\modelmux.exe"""; Flags: runhidden waituntilterminated
Filename: "{app}\nssm.exe"; Parameters: "set ModelMux AppDirectory ""{commonappdata}\ModelMux"""; Flags: runhidden waituntilterminated
Filename: "{app}\nssm.exe"; Parameters: "set ModelMux DisplayName ""ModelMux v3.0 - AI Provider Gateway"""; Flags: runhidden waituntilterminated
Filename: "{app}\nssm.exe"; Parameters: "set ModelMux Description ""去中心化 AI 模型网关联邦网络节点"""; Flags: runhidden waituntilterminated
Filename: "{app}\nssm.exe"; Parameters: "set ModelMux Start SERVICE_AUTO_START"; Flags: runhidden waituntilterminated
Filename: "{app}\nssm.exe"; Parameters: "set ModelMux AppStdout ""{commonappdata}\ModelMux\service.log"""; Flags: runhidden waituntilterminated
Filename: "{app}\nssm.exe"; Parameters: "set ModelMux AppStderr ""{commonappdata}\ModelMux\service-error.log"""; Flags: runhidden waituntilterminated
Filename: "{app}\nssm.exe"; Parameters: "set ModelMux AppRotateFiles 1"; Flags: runhidden waituntilterminated
; 启动服务
Filename: "{app}\nssm.exe"; Parameters: "start ModelMux"; Flags: runhidden waituntilterminated
; 打开浏览器
Filename: "http://localhost:8000"; Description: "打开管理面板"; Flags: postinstall shellexec skipifsilent

[UninstallRun]
; 停止并删除服务
Filename: "{app}\nssm.exe"; Parameters: "stop ModelMux"; Flags: runhidden waituntilterminated
Filename: "{app}\nssm.exe"; Parameters: "remove ModelMux confirm"; Flags: runhidden waituntilterminated
Filename: "{app}\nssm.exe"; Parameters: "stop ModelMux-Tunnel"; Flags: runhidden waituntilterminated
Filename: "{app}\nssm.exe"; Parameters: "remove ModelMux-Tunnel confirm"; Flags: runhidden waituntilterminated

[Code]
// 自定义安装页面 - 配置端口
var
  PortPage: TInputQueryWizardPage;
  TunnelPage: TInputOptionWizardPage;

procedure InitializeWizard;
begin
  // 端口配置页
  PortPage := CreateInputQueryPage(wpSelectDir,
    '服务配置', '配置 ModelMux 服务参数',
    '请设置服务端口号（默认 8000）');
  PortPage.Add('HTTP 端口:', False);
  PortPage.Values[0] := '8000';

  // Cloudflare Tunnel 配置页
  TunnelPage := CreateInputOptionPage(PortPage.ID,
    '内网穿透', '配置公网访问',
    '是否配置 Cloudflare Tunnel 以获取公网访问地址？',
    True, False);
  TunnelPage.Add('安装 Cloudflare Tunnel（免费，自动配置 Quick Tunnel）');
  TunnelPage.Add('跳过（仅内网访问）');
  TunnelPage.Values[0] := True;
end;

function NextButtonClick(CurPageID: Integer): Boolean;
begin
  Result := True;
  if CurPageID = PortPage.ID then
  begin
    if PortPage.Values[0] = '' then
    begin
      MsgBox('请输入端口号', mbError, MB_OK);
      Result := False;
    end;
  end;
end;

// 安装完成后执行
procedure CurStepChanged(CurStep: TSetupStep);
var
  Port: String;
begin
  if CurStep = ssPostInstall then
  begin
    Port := PortPage.Values[0];
    // 设置端口环境变量
    RegWriteStringValue(HKEY_LOCAL_MACHINE,
      'SYSTEM\CurrentControlSet\Control\Session Manager\Environment',
      'MODELMUX_PORT', Port);
  end;
end;
