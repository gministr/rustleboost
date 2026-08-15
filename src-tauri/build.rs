fn main() {
    // Embeds app.manifest so Windows requests administrator elevation itself
    // when the .exe launches — the standard mechanism for this, documented at
    // https://learn.microsoft.com/windows/win32/sbscs/application-manifests.
    //
    // The manifest file existed before this line did; main.rs instead detected
    // elevation by probing whether the SAM registry hive was readable, and on
    // failure spawned a hidden PowerShell running
    // `Start-Process ... -Verb RunAs` to relaunch itself elevated. Reading a
    // security hive to test privilege and then self-relaunching via a hidden
    // shell is a well-known technique real malware uses to elevate — visible
    // proxy tooling had no reason to look that way when the manifest already
    // did the same job with zero code and zero child process.
    #[cfg(target_os = "windows")]
    {
        let attrs = tauri_build::Attributes::new().windows_attributes(
            tauri_build::WindowsAttributes::new()
                .app_manifest(include_str!("app.manifest")),
        );
        tauri_build::try_build(attrs).expect("failed to run tauri-build");
    }
    #[cfg(not(target_os = "windows"))]
    {
        tauri_build::build();
    }
}
