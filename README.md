# Matrix Helper

A Go application to manage Matrix chat rooms.

## Features

- **Leave rooms**: Automatically leave inactive Matrix rooms based on inactivity period and room name patterns
  - Two-phase approach: First identify rooms to leave, then confirm before proceeding
  - Intelligently handles rooms with closing keywords in names (close, resolved, completed, done)
  - Shows detailed information about each room before taking action
  
- **Find Rooms with Mentions**: Locate rooms where you were mentioned within a specific time period
  - Get direct links to both Element client and web interfaces
  
- **Mark All Rooms as Read**: Easily mark all rooms as read with a single command

## Requirements

- Go 1.20 or higher
- Matrix account credentials

## Installation

### Install from source

```bash
# Clone the repository
git clone https://github.com/laduwka/matrix-helper.git
cd matrix-helper

# Build the binary
make build

# Run the app
./matrix-helper
```

### Download from GitHub Releases

You can download pre-built binaries for your platform from the [GitHub Releases page](https://github.com/laduwka/matrix-helper/releases).

## Configuration

The application uses environment variables for configuration:

```bash
# Matrix configuration
export MATRIX_DOMAIN="matrix.org"
export MATRIX_USERNAME="your_username"
export MATRIX_PASSWORD="your_password"

# Optional logging configuration
export LOG_LEVEL="info"  # debug, info, warn, error
```

## License

MIT License

## Credits

This project uses the following libraries:

- [gomatrix](https://github.com/matrix-org/gomatrix) - Matrix client for Go
