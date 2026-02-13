# Matrix Helper

**English** | [Русский](README.ru.md)

A Go CLI tool for managing Matrix chat rooms: leave inactive rooms, find mentions, mark all as read.

## Features

- **Leave rooms by keywords**: Leave rooms with closing keywords in their names (`close`, `done`, `resolved`, `completed`), with optional inactivity check
- **Leave all inactive rooms**: Leave all rooms with no activity for a specified period, regardless of name
- **Find mentions**: Locate rooms where you were mentioned, with direct links to Element and web interfaces
- **Mark all as read**: Clear unread status for all rooms with a single command
- **Interactive mode**: Enter credentials via terminal with masked password input
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

## Usage

### CLI flags

| Flag | Description |
|------|-------------|
| `-h`, `--help` | Display help |
| `--version` | Display version |
| `-i` | Interactive mode (prompt for credentials) |
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
```

## License

MIT License

## Credits

- [gomatrix](https://github.com/matrix-org/gomatrix) - Matrix client for Go
- [cenkalti/backoff](https://github.com/cenkalti/backoff) - Retry with exponential backoff
- [sony/gobreaker](https://github.com/sony/gobreaker) - Circuit breaker
- [logrus](https://github.com/sirupsen/logrus) - Logging
- [fatih/color](https://github.com/fatih/color) - Colored terminal output
