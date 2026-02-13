# Matrix Helper

**English** | [Русский](README.ru.md)

A Go CLI tool for managing Matrix chat rooms: leave inactive rooms, find mentions, mark all as read.

## Features

- **Leave rooms by keywords**: Leave rooms with closing keywords in their names (`close`, `done`, `resolved`, `completed`), with optional inactivity check
- **Leave all inactive rooms**: Leave all rooms with no activity for a specified period, regardless of name
- **Find mentions**: Locate rooms where you were mentioned, with direct links to Element and web interfaces
- **Mark all as read**: Clear unread status for all rooms with a single command
- **Interactive mode**: Enter credentials via terminal with masked password input
- **Session caching**: Access token is cached after first login; subsequent runs reuse it without re-prompting
- **Reliable API handling**: Built-in retry with exponential backoff, circuit breaker, and rate limiter

## Requirements

- Go 1.24 or higher
- Matrix account credentials

## Installation

### Install from source

```bash
git clone https://github.com/laduwka/matrix-helper.git
cd matrix-helper
make build
./matrix-helper --version
```

### Download from GitHub Releases

Pre-built binaries for your platform are available on the [GitHub Releases page](https://github.com/laduwka/matrix-helper/releases).

## Configuration

### Environment variables

```bash
# Required
export MATRIX_DOMAIN="matrix.bingo-boom.ru"
export MATRIX_USERNAME="your_username"
export MATRIX_PASSWORD="your_password"

# Optional
export LOG_LEVEL="info"    # debug, info, warn, error
export LOG_FORMAT="text"   # text, json
export LOG_FILE=""         # log file path (defaults to stdout)
```

### Interactive mode

Run with `-i` flag to enter credentials manually:

```bash
matrix-helper -i
```

On first run, you'll be prompted for domain, username, and password. After a successful login the access token is cached locally, so subsequent runs skip the credential prompt. Use `--logout` to revoke the cached session.

## Usage

### CLI flags

| Flag | Description |
|------|-------------|
| `-h`, `--help` | Display help |
| `--version` | Display version |
| `-i` | Interactive mode (prompt for credentials) |
| `--logout` | Revoke cached session and remove stored token |
| `--leave` | Leave rooms mode (by closing keywords) |
| `--leave-inactive` | Leave all inactive rooms mode |
| `--days=N` | Number of days of inactivity |
| `--no-confirm` | Skip confirmation (for cron/scripts) |
| `--mentions` | Find mentions mode |
| `--mention-days=N` | Days to look back for mentions (default: 30) |
| `--mark-read` | Mark all rooms as read |

### Examples

```bash
# Leave rooms with closing keywords, inactive for 60 days
matrix-helper --leave --days=60

# Leave ALL inactive rooms (30 days)
matrix-helper --leave-inactive --days=30

# Non-interactive mode for cron
matrix-helper --leave-inactive --days=90 --no-confirm

# Find mentions in the last week
matrix-helper --mentions --mention-days=7

# Mark all rooms as read
matrix-helper --mark-read

# Interactive mode
matrix-helper -i

# Logout (revoke cached token)
matrix-helper --logout
```

## Session Caching

In interactive mode (`-i`), the access token is cached after the first successful login:

- **Location**: `$XDG_RUNTIME_DIR/matrix-helper/token.json` (falls back to `~/.cache/matrix-helper/token.json`)
- **Permissions**: directory `0700`, file `0600` (owner-only access)
- **Validation**: the cached token is verified via the Matrix `/account/whoami` endpoint on each run; if invalid, you'll be prompted for credentials again
- **No password stored**: only the access token is cached, never your password
- **Logout**: run `matrix-helper --logout` to revoke the token server-side and remove the local cache

On systems using `$XDG_RUNTIME_DIR` (most Linux distributions), the token is stored on tmpfs and automatically cleared on logout/reboot.

## License

MIT License

## Credits

- [gomatrix](https://github.com/matrix-org/gomatrix) - Matrix client for Go
- [cenkalti/backoff](https://github.com/cenkalti/backoff) - Retry with exponential backoff
- [sony/gobreaker](https://github.com/sony/gobreaker) - Circuit breaker
- [golang.org/x/time/rate](https://pkg.go.dev/golang.org/x/time/rate) - Rate limiter
- [golang.org/x/sync](https://pkg.go.dev/golang.org/x/sync) - Concurrency primitives (errgroup, semaphore)
- [logrus](https://github.com/sirupsen/logrus) - Logging
- [fatih/color](https://github.com/fatih/color) - Colored terminal output
