# ezNAS

**Turn an old Windows or Linux computer into a simple, self-hosted NAS.**

ezNAS is a lightweight file server with a browser-based file manager. It is designed to keep your files on your own device, with no account or cloud service required.

> **Development status:** early development. Do not expose an internet-facing instance or store your only copy of important files in ezNAS yet. Review the security notes below.

## Current features

- Go server with the web UI embedded in the executable.
- Windows and Linux builds (amd64 and arm64).
- Browse folders, upload and download files, create folders, rename and delete items.
- Search file and folder names.
- Optional password gate with random salt and repeated SHA-256 hashing; use a strong unique password.
- Configurable host, port and storage directory through a JSON config.
- Upload limit of 10 GiB per file.
- Path traversal checks and safeguards against symlinks escaping the storage directory.
- Linux storage-capacity display.
- One-command installers that download published releases and verify SHA-256 checksums.

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

For security-conscious installations, inspect the installer script before executing it. The installer verifies the downloaded release asset against the published SHA-256 checksum file.

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

The password command currently uses a basic terminal prompt with visible input; do not run it where others can see the terminal. Restart the server after changing the password. Password protection is disabled by default for initial local testing.

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

- Password protection is optional and disabled by default. Set a password before allowing access from other people or networks.
- The current HTTP server does not provide TLS. Keep it on a trusted LAN or put it behind a properly configured HTTPS reverse proxy. Do not port-forward it directly to the internet.
- Password sessions are in-memory and expire after 12 hours; restarting the server invalidates sessions.
- Deleting files is permanent. There is no recycle bin or backup system yet.
- Keep independent backups of important data.
- Review this project's security posture before using it for sensitive data.

## Development

```sh
go test ./...
go vet ./...
```

Contributions and bug reports are welcome. See [LICENSE](LICENSE).
