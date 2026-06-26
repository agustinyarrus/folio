; Folio — instalador (Inno Setup 6). Compilar: ISCC folio.iss  ->  dist\Folio-Setup-x.y.z.exe
#define AppName    "Folio"
#define AppVer     "1.0.0"
#define AppExe     "folio.exe"
#define AppPub     "Agustin Yarrus"
#define AppUrl     "https://github.com/agustinyarrus/folio"
#define Exts       ".md;.markdown;.mdown;.mkd;.mkdn;.mdwn;.mdtxt;.mdtext;.text"

[Setup]
AppId={{5C1A8E92-7B4F-4A3D-8C26-9D0E3F1B7A62}
AppName={#AppName}
AppVersion={#AppVer}
AppPublisher={#AppPub}
AppPublisherURL={#AppUrl}
AppSupportURL={#AppUrl}
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
UninstallDisplayIcon={app}\{#AppExe}
LicenseFile=LICENSE
OutputDir=dist
OutputBaseFilename={#AppName}-Setup-{#AppVer}
SetupIconFile=folio.ico
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
MinVersion=10.0
AppReadmeFile={app}\README.md

[Languages]
Name: "es"; MessagesFile: "compiler:Languages\Spanish.isl"
Name: "en"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked
Name: "openwith";    Description: "Registrar {#AppName} en el menu ""Abrir con"" para Markdown"; GroupDescription: "Integracion con Windows:"

[Files]
Source: "{#AppExe}";  DestDir: "{app}"; Flags: ignoreversion
Source: "folio.ico";  DestDir: "{app}"; Flags: ignoreversion
Source: "README.md";  DestDir: "{app}"; Flags: ignoreversion isreadme
Source: "LICENSE";    DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#AppName}";              Filename: "{app}\{#AppExe}"
Name: "{group}\Desinstalar {#AppName}";  Filename: "{uninstallexe}"
Name: "{autodesktop}\{#AppName}";        Filename: "{app}\{#AppExe}"; Tasks: desktopicon

[Registry]
Root: HKA; Subkey: "Software\Classes\Applications\{#AppExe}"; ValueType: string; ValueName: "FriendlyAppName"; ValueData: "{#AppName}"; Flags: uninsdeletekey; Tasks: openwith
Root: HKA; Subkey: "Software\Classes\Applications\{#AppExe}\DefaultIcon"; ValueType: string; ValueData: "{app}\{#AppExe},0"; Tasks: openwith
Root: HKA; Subkey: "Software\Classes\Applications\{#AppExe}\shell\open\command"; ValueType: string; ValueData: """{app}\{#AppExe}"" ""%1"""; Tasks: openwith

[Run]
Filename: "{app}\{#AppExe}"; Description: "Abrir {#AppName} ahora"; Flags: nowait postinstall skipifsilent

[Code]
procedure AddSupportedTypes(exeName, csv: String);
var rk: Integer; key, ext: String; p: Integer;
begin
  if IsAdminInstallMode then rk := HKLM else rk := HKCU;
  key := 'Software\Classes\Applications\' + exeName + '\SupportedTypes';
  csv := csv + ';';
  repeat
    p := Pos(';', csv);
    ext := Trim(Copy(csv, 1, p-1));
    Delete(csv, 1, p);
    if ext <> '' then RegWriteStringValue(rk, key, ext, '');
  until Length(csv) = 0;
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if (CurStep = ssPostInstall) and WizardIsTaskSelected('openwith') then
    AddSupportedTypes('{#AppExe}', '{#Exts}');
end;
