; Script de Instalación Optimizado - Agente IoT TecnoData
; -------------------------------------------------------

#define MyAppName "Agente IoT Monitor"
#define MyAppVersion "1.3"
#define MyAppPublisher "TecnoData"
#define MyAppExeName "AgentSNMP.exe"
#define MyAppIcon "logo.ico" 

[Setup]
AppId={{A59837B0-9C33-4A5C-8E1B-9F22E6459123}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
DefaultDirName={autopf}\{#MyAppName}
OutputBaseFilename=Instalador_Agente_TecnoData
Compression=lzma
SolidCompression=yes
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64

; --- ICONOS ---
SetupIconFile={#MyAppIcon}
UninstallDisplayIcon={app}\{#MyAppExeName}

[Languages]
Name: "spanish"; MessagesFile: "compiler:Languages\Spanish.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
; El ejecutable que compilaste con el nuevo diseño y encriptación
Source: "AgentSNMP.exe"; DestDir: "{app}"; Flags: ignoreversion
; Copiamos el icono por si el sistema necesita refrescar la caché
Source: "{#MyAppIcon}"; DestDir: "{app}"; Flags: ignoreversion

Source: "C:\Users\asaavedra\Desktop\agentsnmpppp\profiles\*"; DestDir: "{app}\profiles"; Flags: ignoreversion recursesubdirs createallsubdirs

[Dirs]
; Carpeta para logs y config encriptada
Name: "{commonappdata}\AgentSNMP"; Permissions: users-modify

[Icons]
Name: "{autoprograms}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; IconFilename: "{app}\{#MyAppIcon}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon; IconFilename: "{app}\{#MyAppIcon}"

[Run]
; 1. Instala el servicio de Windows
Filename: "{app}\{#MyAppExeName}"; Parameters: "install"; Flags: runhidden waituntilterminated
; 2. Inicia el servicio (quedará en loop esperando la Key encriptada)
Filename: "{app}\{#MyAppExeName}"; Parameters: "start"; Flags: runhidden waituntilterminated
; 3. Abre la GUI (con el nuevo diseño de Tabs) para que el usuario configure
Filename: "{app}\{#MyAppExeName}"; Description: "Configurar Agente Ahora"; Flags: nowait postinstall skipifsilent

[UninstallRun]
; Detiene y elimina el servicio limpiamente
Filename: "{app}\{#MyAppExeName}"; Parameters: "stop"; Flags: runhidden waituntilterminated
Filename: "{app}\{#MyAppExeName}"; Parameters: "uninstall"; Flags: runhidden waituntilterminated

[UninstallDelete]
; 🔴 OPCIONAL: Borra la configuración y logs al desinstalar para que no queden datos "zombie"
Type: filesandordirs; Name: "{commonappdata}\AgentSNMP"
