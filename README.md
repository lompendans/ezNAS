# ezNAS

**Turn an old Windows or Linux computer into a simple, self-hosted NAS.**

ezNAS is a lightweight file server with a browser-based file manager. It is designed to keep your files on your own device, with no account or cloud service required.

> **Development status:** early development. Do not expose an internet-facing instance or store your only copy of important files in ezNAS yet. Review the security notes below.

## Current features

- Go server with the web UI embedded in the executable.
- Windows and Linux builds (amd64 and arm64).
- Browse folders, upload and download files, create folders, rename and delete items.
- Search file and folder names.
- Mandatory password gate: first run generates a one-time high-entropy password; users can set their own with `eznas password set`.
- Configurable host, port and storage directory through a JSON config.
- Upload limit of 10 GiB per file.
- Path traversal checks and safeguards against symlinks escaping the storage directory.
- Linux storage-capacity display.
- One-command installers that download published releases and verify SHA-256 checksums.
- Update checks in the web UI, plus `eznas update` to download the latest stable release, verify its SHA-256 checksum, and stage the executable without touching the currently installed binary.
- Windows installer adds ezNAS to the current user's PATH and can optionally start it automatically at sign-in.

## Install

Installers download the **latest published GitHub release**. They will not install anything until a release has been published.

### Linux (x86-64 / ARM64)

```sh
curl -fsSL https://raw.githubusercontent.com/lompendans/ezNAS/main/install.sh | sh
```

### Windows PowerShell (x86-64 / ARM64)

```powershell
irm https://raw.githubusercontent.com/lompendans/ezNAS/main/install.ps1 | iex
```

For security-conscious installations, inspect the installer script before executing it. The installer verifies the downloaded release asset against the published SHA-256 checksum file. On Windows, it adds the install directory to your user PATH and offers optional automatic startup at sign-in. Open a new terminal after installation so the updated PATH is picked up.

## Run from source

Requires Go 1.23 or newer.

```sh
go build -o eznas .
./eznas
```

On Windows:

```powershell
go build -o eznas.exe .
.\eznas.exe
```

The web interface is available at `http://localhost:8080` by default. On a private LAN, use the host machine's LAN address and ensure the network firewall allows access only where you intend.

## Commands

```text
eznas             Start the server in the foreground
eznas start       Start the server in the foreground
eznas status      Check the local HTTP endpoint
eznas storage     Show the storage path and available capacity where supported
eznas config      Show safe configuration details
eznas password set Enable or change password protection
eznas version     Show version
eznas help        Show commands
```

Password protection is always enabled. On the first run, ezNAS generates a strong random password and prints it once in the terminal; save it before closing the terminal. To set or change the password, stop ezNAS and run `eznas password set`, then restart the server. The current password prompt displays typed characters, so do this where others cannot see your terminal.

## Updating

The web interface checks GitHub for the latest stable release on startup and periodically. If a newer version is available, it shows a link to the release page.

You can also run:

```sh
eznas update
```

This downloads the matching Windows or Linux release archive and `checksums.txt`, verifies the archive's SHA-256 hash, extracts the expected executable, and stages it under the ezNAS user configuration directory at `updates/<release-tag>/`. **This phase does not replace the installed executable or restart a running server.** Keep using the current installation until the safe apply/restart helper is implemented in a later phase. If the release is already installed, the command reports that and exits.

## Configuration

Default config path:
- Windows: `%AppData%\eznas\config.json` (as provided by Go's user config directory)
- Linux: typically `~/.config/eznas/config.json`

Override the location with `EZNAS_CONFIG=/path/to/config.json`.

Example:

```json
{
  "host": "0.0.0.0",
  "port": 8080,
  "storage": "/srv/eznas"
}
```

Use an absolute storage path for a durable installation. The default storage directory is `./eznas-data` relative to the process working directory. ezNAS does not format drives or automatically delete data.

## Security notes

- Password protection is mandatory and enabled on first run. Keep the generated password private and change it to a strong unique password if desired.
- The current HTTP server does not provide TLS. Keep it on a trusted LAN or put it behind a properly configured HTTPS reverse proxy. Do not port-forward it directly to the internet.
- Password sessions are in-memory and expire after 12 hours; restarting the server invalidates sessions.
- Deleting files is permanent. There is no recycle bin or backup system yet.
- Keep independent backups of important data.
- Review this project's security posture before using it for sensitive data.

## Windows Network and native file sharing

Windows users can configure native File Explorer discovery and SMB sharing with the elevated PowerShell setup script in the repository:

```powershell
# Open PowerShell as Administrator in the downloaded/cloned repository folder
.\setup-network.ps1
```

The script uses Windows' built-in SMB server, creates the `ezNAS` share for the current Windows account, enables the Windows discovery services where available, and creates firewall rules scoped to the **Private** network profile and local subnet. It asks for the absolute storage path and web port, and saves the settings for ezNAS. Restart ezNAS after running it.

- The web interface uses the ezNAS password.
- File Explorer SMB access uses the Windows account credentials granted share access; these are separate authentication systems. This avoids anonymous SMB access, but a user who has Windows share credentials can access the files without logging in to the web UI.
- Set your LAN network profile to **Private** and enable Network discovery in Windows Settings if needed. Do not enable SMB access on Public networks or forward TCP 445 from the internet.
- This setup script requires Windows editions with SMB server cmdlets and Administrator rights. It has not yet been tested on every Windows edition.

The current HTTP server does not itself publish a native SMB share; the setup script configures Windows' native SMB service for this. On Linux, use Samba for native SMB sharing; the installer does not automatically change Samba configuration.

The web file manager and SMB share both operate on the configured storage directory when Windows setup has completed successfully, so changes made through either interface should be visible through the other. SMB permissions are managed separately from the ezNAS web password. Only grant SMB access to trusted Windows accounts; a user with SMB access can read and modify shared files without logging into the web interface.

## Development

```sh
go test ./...
go vet ./...
```

Contributions and bug reports are welcome. See [LICENSE](LICENSE).
