# ezNAS

**Turn an old computer into your personal or family network NAS.**

ezNAS is a lightweight, self-hosted file hub that repurposes an old Windows PC, Linux machine, mini PC, or single-board computer as shared storage on your home network. Your files stay on the device you choose; ezNAS does not require a cloud account or send files to a hosted storage provider.

> **Development status:** early development. Do not expose an internet-facing instance or store your only copy of important files in ezNAS yet. Review the security notes below.

## Current features

- Go server with the web UI embedded in the executable.
- Windows and Linux builds (amd64 and arm64).
- Browse folders, upload and download files, create folders, rename items, and move deleted items to a recoverable recycle bin.
- Restore recycle-bin items or permanently empty the recycle bin.
- Create and download ZIP backups of the storage directory from the web UI.
- Optional built-in HTTPS/TLS with self-signed or externally issued certificates; trusted HTTPS reverse-proxy deployment is also supported.
- Search file and folder names.
- Create expiring, revocable, read-only download links for individual files.
- Rate-limit failed password attempts, reject cross-origin browser writes, and persist share secrets as hashes rather than raw tokens.
- Limit concurrent uploads and backups to avoid overwhelming low-power devices.
- Mandatory password gate: first run generates a one-time high-entropy password; users can set their own with `eznas password set`.
- Configurable host, port and storage directory through a JSON config.
- Upload limit of 10 GiB per file.
- Path traversal checks and safeguards against symlinks escaping the storage directory.
- Linux storage-capacity display.
- One-command installers that download published releases and verify SHA-256 checksums.
- Update checks in the web UI, a one-click installer, and optional automatic installation with SHA-256 verification and rollback support.
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

For security-conscious installations, inspect the installer script before executing it. The installer verifies the downloaded release asset against the published SHA-256 checksum file. On Windows, it adds the install directory to your user PATH, offers optional trusted local HTTPS setup, and offers automatic startup at sign-in. The HTTPS option generates a local self-signed certificate and adds it to the current Windows user's trusted root store. Open a new terminal after installation so the updated PATH is picked up.

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

## Recycle bin, backups, and HTTPS

### Recycle bin
Deleting a file or folder moves it to a hidden `.eznas-trash` directory inside the configured storage root. Use the web UI's **Recycle bin** view to restore items or empty the bin permanently. Restoring requires that the original path is available; ezNAS will not overwrite an existing file or folder.

### Backups
Open **Backups** and select **Create backup** to make a ZIP snapshot of the configured storage directory. Backups are stored in the ezNAS config directory under `backups` by default (or the configured `backup_dir`) and can be downloaded from the UI. The recycle bin is excluded. Keep a copy on another disk or device: a backup on the same drive does not protect against drive failure. Backups can be large and require enough free space.

### HTTPS / TLS
By default, ezNAS serves HTTP. For a local self-signed certificate, stop ezNAS and run:

```sh
eznas tls generate
```

Restart ezNAS. The command writes a certificate and private key into the config directory and updates the configuration to use them. Browsers will show a trust warning until the certificate is trusted; this is expected for self-signed certificates.

On Windows, you can add the generated self-signed certificate to the **current user's** trusted root store with:

```powershell
eznas tls trust
```

This command only accepts a valid self-signed certificate and does not require administrator elevation. Restart your browser and visit `https://localhost:8080`. The certificate includes localhost, the machine hostname, loopback addresses, and IP addresses present when it was generated; if you later regenerate it or access a new IP address, trust/name matching may need to be configured again. Trusting a self-signed certificate is a deliberate security decision: only trust a certificate you generated yourself. This command currently supports Windows; on Linux, use your distribution's certificate trust-store tooling.

To use a certificate issued by a trusted CA, including a Let's Encrypt certificate issued by Certbot or another ACME client, stop ezNAS and point it at the certificate and matching private key:

```sh
eznas tls use /path/to/fullchain.pem /path/to/privkey.pem
eznas tls status
```

Use the actual paths on your operating system. The certificate and key must form a valid, currently valid pair. Restart ezNAS after configuring the pair. Keep the private key readable only by the account running ezNAS. If Certbot or another ACME client renews the certificate at those same paths, restart ezNAS after renewal so Go reloads the renewed certificate.

Alternatively, put ezNAS behind a properly configured HTTPS reverse proxy such as Caddy, which can obtain and renew Let's Encrypt certificates automatically. In that setup, keep ezNAS bound to a trusted local interface and configure the proxy to forward traffic to it. Do not expose an HTTP-only instance to untrusted networks. **Automatic Let's Encrypt issuance and renewal directly inside ezNAS is not implemented yet**; use an ACME client or reverse proxy for that workflow.

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


## Sharing files with family and friends

Select **Share** beside a file in the web file manager and choose an expiry of 24 hours, 7 days, or 30 days. ezNAS generates a cryptographically random, unguessable link. The link allows a download of that single file only; it does not grant access to the file manager, other folders, uploads, or backups. The raw link is shown only once at creation time, so copy it then. Open **Shared links** to see active links and revoke any of them immediately. Expired links are rejected automatically.

Share records are stored in `shares.json` beside the ezNAS configuration, with only a SHA-256 digest of each secret token. If a shared file is deleted, moved, replaced, or modified, its old link stops working. Folder sharing and separate family-member accounts/permissions are not included in this first sharing release.

For a home/family setup, keep ezNAS on a private LAN, use HTTPS, and share links only with people you trust. A share link is a bearer credential: anyone who receives or forwards it can download that file. Do not post private links publicly.

## Updating

The web interface checks GitHub for the latest stable release on startup and periodically. If a newer version is available, it shows an update banner.

You can also run:

```sh
eznas update
```

This downloads the matching Windows or Linux release archive and `checksums.txt`, verifies the archive's SHA-256 hash, extracts the expected executable, and stages it under the ezNAS user configuration directory at `updates/<release-tag>/`. A separate platform helper waits for the updater process to exit, stops a running server, backs up the current executable, installs the verified binary, and attempts to restart ezNAS. If the new version fails to launch, the helper restores the previous executable. The backup is retained as `<executable>.eznas-backup` for manual recovery.

The web UI's **Install update** button runs the same workflow. The **Install automatically** checkbox is an opt-in preference stored in that browser; when enabled, ezNAS applies a detected stable update during a subsequent update check. Updates may briefly interrupt connections. Configuration and storage directories are not modified by the updater. Linux systemd user installations are restarted through systemd when the `eznas.service` unit exists.

Automatic updating is supported for Windows and Linux on amd64 and arm64. Review release notes and keep backups of important files regardless of updater safeguards.

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
- If TLS is not configured, the server uses HTTP. Keep it on a trusted LAN or put it behind a properly configured HTTPS reverse proxy. Do not port-forward an HTTP-only instance directly to the internet.
- Password sessions are in-memory and expire after 12 hours; restarting the server invalidates sessions. Failed logins are rate-limited per client IP, and browser write requests are checked for same-origin requests.
- A single ezNAS administrator password protects the web file manager. Share links are separate, high-entropy bearer links that are read-only, expire within 30 days, and can be revoked. Anyone who obtains a link can download that one file until it expires or is revoked; share links are not a substitute for private account permissions.
- The recycle bin is recoverable but lives on the same storage device. Backups are also local by default; copy them to another physical device for disaster recovery.
- ezNAS limits simultaneous uploads and backups to reduce resource contention on older hardware. Very large uploads and backups still require sufficient free disk space.
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
