package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/laduwka/matrix-helper/app"
	"github.com/laduwka/matrix-helper/helper"
	"github.com/sirupsen/logrus"
	"golang.org/x/term"
)

var version = "dev"

type CLI struct {
	actions app.ActionService
	log     *logrus.Logger
}

func main() {

	helpFlag := flag.Bool("help", false, "Display help information")
	hFlag := flag.Bool("h", false, "Display help information (shorthand)")
	versionFlag := flag.Bool("version", false, "Display version information")

	leaveFlag := flag.Bool("leave", false, "Leave rooms mode")
	leaveInactiveDays := flag.Int("days", 0, "Number of days of inactivity (with --leave)")
	noConfirm := flag.Bool("no-confirm", false, "Skip confirmation prompts")

	mentionsFlag := flag.Bool("mentions", false, "Find rooms with mentions mode")
	mentionsDays := flag.Int("mention-days", 30, "Days to look back for mentions")

	leaveInactiveFlag := flag.Bool("leave-inactive", false, "Leave all inactive rooms mode")

	markReadFlag := flag.Bool("mark-read", false, "Mark all rooms as read mode")

	interactiveFlag := flag.Bool("i", false, "Interactive mode with credential prompting")

	logoutFlag := flag.Bool("logout", false, "Revoke cached session and remove stored token")

	flag.Parse()

	if *helpFlag || *hFlag {
		printHelp()
		return
	}

	if *versionFlag {
		fmt.Printf("matrix-helper %s\n", version)
		return
	}

	config := &RunConfig{
		LeaveMode:         *leaveFlag,
		LeaveInactiveMode: *leaveInactiveFlag,
		LeaveInactiveDays: *leaveInactiveDays,
		NoConfirm:         *noConfirm,
		MentionsMode:      *mentionsFlag,
		MentionsDays:      *mentionsDays,
		MarkReadMode:      *markReadFlag,
		Interactive:       *interactiveFlag,
		Logout:            *logoutFlag,
	}

	if err := run(config); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

type RunConfig struct {
	LeaveMode         bool
	LeaveInactiveMode bool
	MentionsMode      bool
	MarkReadMode      bool
	Interactive       bool
	Logout            bool

	LeaveInactiveDays int
	MentionsDays      int
	NoConfirm         bool
}

func printHelp() {
	fmt.Println("Matrix Helper - A tool for managing Matrix chat rooms")
	fmt.Println("\nUSAGE:")
	fmt.Println("  matrix-helper [flags]")
	fmt.Println("  matrix-helper                  # Run in interactive mode")
	fmt.Println("  matrix-helper --leave --days=30 # Leave inactive rooms with closing keywords")
	fmt.Println("  matrix-helper --mentions --mention-days=7 # Find recent mentions")
	fmt.Println("  matrix-helper --mark-read      # Mark all rooms as read")

	fmt.Println("\nGENERAL FLAGS:")
	fmt.Println("  -h, --help     Display this help message")
	fmt.Println("  --version      Display version information")
	fmt.Println("  -i             Interactive mode (prompt for credentials)")
	fmt.Println("  --logout       Revoke cached session and remove stored token")

	fmt.Println("\nLEAVE ROOMS MODE:")
	fmt.Println("  --leave        Activate leave rooms mode")
	fmt.Println("  --days=N       Number of days of inactivity to check (default: 0)")
	fmt.Println("                 Use 0 to only check room names for closing keywords")
	fmt.Println("  --no-confirm   Skip confirmation prompt before leaving rooms")

	fmt.Println("\nLEAVE INACTIVE ROOMS MODE:")
	fmt.Println("  --leave-inactive   Leave ALL rooms inactive for N days")
	fmt.Println("  --days=N           Number of days of inactivity (required, must be > 0)")
	fmt.Println("  --no-confirm       Skip confirmation prompt")

	fmt.Println("\nFIND MENTIONS MODE:")
	fmt.Println("  --mentions         Activate find mentions mode")
	fmt.Println("  --mention-days=N   Number of days to check for mentions (default: 30)")

	fmt.Println("\nMARK READ MODE:")
	fmt.Println("  --mark-read    Mark all rooms as read")

	fmt.Println("\nFEATURES:")
	fmt.Println("  1. Leave rooms")
	fmt.Println("     - Leave rooms with closing keywords in their names")
	fmt.Println("     - Optionally check for inactivity period")
	fmt.Println("     - Closing keywords: 'close', 'resolved', 'completed', 'done'")
	fmt.Println("\n  2. Find Rooms with Mentions")
	fmt.Println("     - Find rooms where you've been mentioned")
	fmt.Println("     - Specify time period to check")
	fmt.Println("\n  3. Mark All Rooms as Read")
	fmt.Println("     - Clear unread status for all rooms")

	fmt.Println("\nCONFIGURATION:")
	fmt.Println("  Set these environment variables:")
	fmt.Println("  - MATRIX_DOMAIN      Matrix homeserver domain (required)")
	fmt.Println("  - MATRIX_USERNAME    Your matrix username (required)")
	fmt.Println("  - MATRIX_PASSWORD    Your matrix password (required)")
	fmt.Println("  - LOG_LEVEL          Logging level (default: info)")
	fmt.Println("                       [debug, info, warn, error]")

	fmt.Println("\nEXAMPLES:")
	fmt.Println("  # Leave rooms with 'close' in name and inactive for 60 days:")
	fmt.Println("  matrix-helper --leave --days=60")
	fmt.Println("\n  # Find mentions in the last week:")
	fmt.Println("  matrix-helper --mentions --mention-days=7")
	fmt.Println("\n  # Run script in non-interactive mode (for cron jobs):")
	fmt.Println("  matrix-helper --leave --days=90 --no-confirm")
}

var (
	titlePrintln   = color.New(color.FgCyan, color.Bold).PrintlnFunc()
	successPrintln = color.New(color.FgGreen).PrintlnFunc()
	successPrintf  = color.New(color.FgGreen).PrintfFunc()
	warnPrintln    = color.New(color.FgYellow).PrintlnFunc()
	warnPrintf     = color.New(color.FgYellow).PrintfFunc()
)

var progressFn = func(current, total int, message string) {
	if total == 0 {
		fmt.Printf("  %s\n", message)
	} else {
		fmt.Printf("  %s: %d/%d\n", message, current, total)
	}
}

func run(config *RunConfig) error {
	if config.Logout {
		return runLogout()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	setupSignalHandler(cancel)

	var cli *CLI
	var err error

	hasModeFlag := config.LeaveMode || config.LeaveInactiveMode || config.MentionsMode || config.MarkReadMode
	if !hasModeFlag || config.Interactive {
		cli, err = initializeInteractiveApp()
	} else {
		cli, err = initializeApp()
	}
	if err != nil {
		return fmt.Errorf("failed to initialize application: %w", err)
	}

	if config.LeaveMode {
		return cli.leaveRoomsByDateWithConfig(ctx, config.LeaveInactiveDays, config.NoConfirm)
	} else if config.LeaveInactiveMode {
		if config.LeaveInactiveDays <= 0 {
			return fmt.Errorf("--days must be > 0 when using --leave-inactive")
		}
		return cli.leaveInactiveRoomsWithConfig(ctx, config.LeaveInactiveDays, config.NoConfirm)
	} else if config.MentionsMode {
		return cli.findMentionedRoomsWithConfig(ctx, config.MentionsDays)
	} else if config.MarkReadMode {
		return cli.markAllAsRead(ctx)
	}

	if err := cli.RunCLI(ctx); err != nil {
		return fmt.Errorf("CLI execution failed: %w", err)
	}

	return nil
}

func setupSignalHandler(cancel context.CancelFunc) {
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
		<-quit
		fmt.Println("\nShutting down gracefully...")
		cancel()
	}()
}

func promptCredentials() (domain, username, password string, err error) {
	reader := bufio.NewReader(os.Stdin)

	titlePrintln("Matrix Helper - Interactive Setup")
	titlePrintln("==================================")
	fmt.Println()

	fmt.Print("Matrix homeserver domain [matrix.bingo-boom.ru]: ")
	domain, _ = reader.ReadString('\n')
	domain = strings.TrimSpace(domain)
	if domain == "" {
		domain = "matrix.bingo-boom.ru"
	}

	fmt.Print("Username: ")
	username, _ = reader.ReadString('\n')
	username = strings.TrimSpace(username)
	if username == "" {
		return "", "", "", fmt.Errorf("username is required")
	}

	fmt.Print("Password: ")
	if term.IsTerminal(int(syscall.Stdin)) {
		passwordBytes, err := term.ReadPassword(int(syscall.Stdin))
		if err != nil {
			return "", "", "", fmt.Errorf("failed to read password: %w", err)
		}
		fmt.Println()
		password = string(passwordBytes)
	} else {
		password, _ = reader.ReadString('\n')
		password = strings.TrimSpace(password)
	}

	if password == "" {
		return "", "", "", fmt.Errorf("password is required")
	}

	return domain, username, password, nil
}

func interactiveClientOpts(log *logrus.Logger) []helper.ClientOption {
	return []helper.ClientOption{
		helper.WithRetryOptions(helper.RetryOptions{
			MaxAttempts: 3,
			BaseDelay:   time.Second,
			MaxDelay:    time.Second * 5,
			UseJitter:   true,
		}),
		helper.WithCircuitBreakerOptions(helper.CircuitBreakerOptions{
			Threshold: 5,
			Timeout:   time.Minute * 2,
			Enabled:   true,
			ShouldTrip: func(err error) bool {
				return !strings.Contains(err.Error(), "rate limit") &&
					!strings.Contains(err.Error(), "M_LIMIT_EXCEEDED")
			},
			OnStateChange: func(from, to string) {
				log.Infof("Circuit breaker state changed from %s to %s", from, to)
			},
		}),
		helper.WithRateLimiterOptions(helper.RateLimiterOptions{
			Rate:     20,
			Interval: time.Minute,
			Capacity: 5,
			Enabled:  true,
		}),
	}
}

func initializeInteractiveApp() (*CLI, error) {
	log := app.NewLogger(app.LoggerConfig{Level: "info", Format: "text"})

	// Try cached token first
	cached, err := helper.LoadToken()
	if err != nil {
		warnPrintf("Warning: failed to load cached session: %v\n", err)
	}

	if cached != nil {
		successPrintf("Found cached session for %s\n", cached.UserID)
		successPrintf("Connecting to %s...\n", cached.HomeserverURL)

		client, err := helper.NewClientFromToken(cached, log, interactiveClientOpts(log)...)
		if err != nil {
			warnPrintf("Cached session is invalid: %v\n", err)
			warnPrintln("Falling back to credential prompt...")
			_ = helper.RemoveToken()
			fmt.Println()
		} else {
			successPrintln("Connected successfully!")
			fmt.Println()

			actionService := app.NewActions(
				client,
				log,
				app.WithConcurrency(5),
				app.WithProgress(progressFn),
			)
			return &CLI{actions: actionService, log: log}, nil
		}
	}

	// No valid cached token — prompt for credentials
	domain, username, password, err := promptCredentials()
	if err != nil {
		return nil, err
	}

	cfg := app.LoadFromValues(domain, username, password)
	log = app.NewLogger(cfg.Log.ToLoggerConfig())

	fmt.Println()
	successPrintf("Connecting to %s...\n", cfg.Matrix.HomeserverURL)

	client, err := helper.NewClient(
		cfg.Matrix.ToMatrixConfig(),
		log,
		interactiveClientOpts(log)...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize matrix client: %w", err)
	}

	successPrintln("Connected successfully!")

	// Cache the token for future runs
	token := &helper.CachedToken{
		HomeserverURL: cfg.Matrix.HomeserverURL,
		UserID:        client.UserID(),
		AccessToken:   client.AccessToken(),
		DeviceID:      client.DeviceID(),
		Username:      username,
		Domain:        domain,
		CreatedAt:     time.Now(),
	}
	if err := helper.SaveToken(token); err != nil {
		warnPrintf("Warning: failed to cache session: %v\n", err)
	} else {
		successPrintln("Session cached for future runs")
	}
	fmt.Println()

	actionService := app.NewActions(
		client,
		log,
		app.WithConcurrency(5),
		app.WithProgress(progressFn),
	)

	return &CLI{
		actions: actionService,
		log:     log,
	}, nil
}

func runLogout() error {
	token, err := helper.LoadToken()
	if err != nil {
		return fmt.Errorf("failed to load cached session: %w", err)
	}
	if token == nil {
		fmt.Println("No cached session found.")
		return nil
	}

	fmt.Printf("Logging out session for %s...\n", token.UserID)
	if err := helper.LogoutAndCleanup(token); err != nil {
		warnPrintf("Warning: server-side logout failed: %v\n", err)
		fmt.Println("Local token has been removed.")
		return nil
	}

	successPrintln("Logged out successfully. Cached session removed.")
	return nil
}

func initializeApp() (*CLI, error) {

	cfg, err := app.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	log := app.NewLogger(cfg.Log.ToLoggerConfig())

	retryOpts := helper.RetryOptions{
		MaxAttempts: 3,
		BaseDelay:   time.Second,
		MaxDelay:    time.Second * 5,
		UseJitter:   true,
	}

	cbOpts := helper.CircuitBreakerOptions{
		Threshold: 5,
		Timeout:   time.Minute * 2,
		Enabled:   true,
		ShouldTrip: func(err error) bool {

			return !strings.Contains(err.Error(), "rate limit") &&
				!strings.Contains(err.Error(), "M_LIMIT_EXCEEDED")
		},
		OnStateChange: func(from, to string) {
			log.Infof("Circuit breaker state changed from %s to %s", from, to)
		},
	}

	rlOpts := helper.RateLimiterOptions{
		Rate:     20,
		Interval: time.Minute,
		Capacity: 5,
		Enabled:  true,
	}

	client, err := helper.NewClient(
		cfg.Matrix.ToMatrixConfig(),
		log,
		helper.WithRetryOptions(retryOpts),
		helper.WithCircuitBreakerOptions(cbOpts),
		helper.WithRateLimiterOptions(rlOpts),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize matrix client: %w", err)
	}
	log.Debug("Matrix client initialized")

	actionService := app.NewActions(
		client,
		log,
		app.WithConcurrency(5),
		app.WithProgress(progressFn),
	)

	return &CLI{
		actions: actionService,
		log:     log,
	}, nil
}

func (c *CLI) RunCLI(ctx context.Context) error {
	titlePrintln("Matrix Helper")
	titlePrintln("=============")
	fmt.Println("Choose an action:")
	fmt.Println("  1. Leave rooms (with closing keywords)")
	fmt.Println("  2. Find rooms with mentions")
	fmt.Println("  3. Mark all rooms as read")
	fmt.Println("  4. Leave inactive rooms")
	fmt.Print("Enter your choice (1-4): ")

	var input string
	if _, err := fmt.Scanln(&input); err != nil {
		return fmt.Errorf("failed to read choice: %w", err)
	}

	switch input {
	case "1":
		return c.leaveRoomsByDate(ctx)
	case "2":
		return c.findMentionedRooms(ctx)
	case "3":
		return c.markAllAsRead(ctx)
	case "4":
		return c.leaveInactiveRooms(ctx)
	default:
		return fmt.Errorf("invalid choice: %q (must be 1-4)", input)
	}
}

func (c *CLI) leaveRoomsByDateWithConfig(ctx context.Context, days int, skipConfirm bool) error {
	if days < 0 || days > 365 {
		return fmt.Errorf("days parameter must be between 0 and 365")
	}

	fmt.Printf("Finding rooms to leave (inactive for %d days with closing keywords)...\n", days)

	fmt.Println("Phase 1: Analyzing rooms to determine which should be left...")
	roomsToLeave, roomsToKeep, err := c.actions.FindRoomsToLeave(ctx, days)
	if err != nil {
		return fmt.Errorf("failed to analyze rooms: %w", err)
	}

	if len(roomsToLeave) > 0 {
		fmt.Printf("\nThe following %d rooms will be left:\n", len(roomsToLeave))
		for i, roomInfo := range roomsToLeave {
			fmt.Printf("%2d. %s\n", i+1, roomInfo.Room.Name)
			fmt.Printf("    Reason: %s\n", roomInfo.Reason)
		}

		if !skipConfirm {
			fmt.Print("\nDo you want to proceed with leaving these rooms? (y/n): ")
			var confirmation string
			if _, err := fmt.Scanln(&confirmation); err != nil {
				return fmt.Errorf("failed to read confirmation: %w", err)
			}
			confirmation = strings.ToLower(strings.TrimSpace(confirmation))

			if confirmation != "y" && confirmation != "yes" {
				fmt.Println("Operation cancelled by user.")
				return nil
			}
		}

		fmt.Println("\nPhase 2: Leaving rooms...")
		leftCount, err := c.actions.LeaveRooms(ctx, roomsToLeave)
		if err != nil {
			return fmt.Errorf("error during room leaving process: %w", err)
		}

		fmt.Printf("\nResults:\n")
		if days == 0 {
			fmt.Printf("- Rooms left (with closing keywords in name): %d of %d\n", leftCount, len(roomsToLeave))
			fmt.Printf("- Rooms kept (without closing keywords): %d\n", len(roomsToKeep))
		} else {
			fmt.Printf("- Rooms left (inactive for %d days with closing keywords): %d of %d\n",
				days, leftCount, len(roomsToLeave))
			fmt.Printf("- Rooms kept (either active or without closing keywords): %d\n", len(roomsToKeep))
		}
	} else {
		fmt.Println("\nNo rooms found that meet the criteria for leaving.")
	}

	c.log.Info("Leave rooms action completed successfully")
	return nil
}

func (c *CLI) leaveRoomsByDate(ctx context.Context) error {
	fmt.Println("\nRoom Leaving Logic:")
	fmt.Println("This feature will leave Matrix rooms based on two criteria:")
	fmt.Println("1. Room name contains a closing keyword (\"close\", \"resolved\", \"completed\", \"done\")")
	fmt.Println("2. Room has been inactive for the specified number of days")
	fmt.Println("\nOptions:")
	fmt.Println("- Enter 0: Leave rooms that contain closing keywords in their names only")
	fmt.Println("- Enter N (days): Leave rooms with closing keywords AND no activity for N days")
	fmt.Print("\nEnter number of days (0-365): ")
	var input string
	if _, err := fmt.Scanln(&input); err != nil {
		return fmt.Errorf("failed to read number of days: %w", err)
	}

	days, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil || days < 0 || days > 365 {
		return fmt.Errorf("please enter a valid number between 0 and 365")
	}

	return c.leaveRoomsByDateWithConfig(ctx, days, false)
}

func (c *CLI) findMentionedRoomsWithConfig(ctx context.Context, days int) error {
	if days < 0 {
		return fmt.Errorf("days parameter must be non-negative")
	}

	fmt.Printf("Searching for mentions in the last %d days...\n", days)
	mentionedRooms, err := c.actions.FindMentionedRooms(ctx, days)
	if err != nil {
		return fmt.Errorf("failed to find mentioned rooms: %w", err)
	}

	if len(mentionedRooms) == 0 {
		fmt.Println("No mentions found in the specified time period.")
	} else {
		fmt.Printf("\nFound %d rooms where you were mentioned:\n", len(mentionedRooms))
		for i, room := range mentionedRooms {
			fmt.Printf("%d. %s\n", i+1, room.Name)
			fmt.Printf("   - Element: %s\n", room.ElementLink)
			fmt.Printf("   - Web: %s\n\n", room.WebLink)
		}
	}

	c.log.Info("Find mentions action completed successfully")
	return nil
}

func (c *CLI) findMentionedRooms(ctx context.Context) error {
	fmt.Print("Enter the number of days to check for mentions: ")
	var input string
	if _, err := fmt.Scanln(&input); err != nil {
		return fmt.Errorf("failed to read number of days: %w", err)
	}

	days, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil || days < 0 || days > 365 {
		return fmt.Errorf("please enter a valid number between 0 and 365")
	}

	return c.findMentionedRoomsWithConfig(ctx, days)
}

func (c *CLI) markAllAsRead(ctx context.Context) error {
	fmt.Println("Marking all rooms as read...")

	if err := c.actions.MarkAllRoomsAsRead(ctx); err != nil {
		return fmt.Errorf("failed to mark all rooms as read: %w", err)
	}

	fmt.Println("All rooms marked as read successfully")
	c.log.Info("Mark as read action completed successfully")
	return nil
}

func (c *CLI) leaveInactiveRooms(ctx context.Context) error {
	fmt.Println("\nLeave Inactive Rooms")
	fmt.Println("This feature will leave ALL rooms that have been inactive for a specified number of days.")
	fmt.Print("\nEnter number of days (1-365): ")
	var input string
	if _, err := fmt.Scanln(&input); err != nil {
		return fmt.Errorf("failed to read number of days: %w", err)
	}

	days, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil || days < 1 || days > 365 {
		return fmt.Errorf("please enter a valid number between 1 and 365")
	}

	return c.leaveInactiveRoomsWithConfig(ctx, days, false)
}

func (c *CLI) leaveInactiveRoomsWithConfig(ctx context.Context, days int, skipConfirm bool) error {
	if days <= 0 || days > 365 {
		return fmt.Errorf("days parameter must be between 1 and 365")
	}

	fmt.Printf("Finding all rooms inactive for %d days...\n", days)

	roomsToLeave, roomsToKeep, err := c.actions.FindInactiveRoomsToLeave(ctx, days)
	if err != nil {
		return fmt.Errorf("failed to analyze rooms: %w", err)
	}

	if len(roomsToLeave) == 0 {
		fmt.Println("\nNo inactive rooms found for the specified period.")
		return nil
	}

	warnPrintf("\nWARNING: The following %d rooms will be left (this action is irreversible):\n", len(roomsToLeave))
	for i, roomInfo := range roomsToLeave {
		fmt.Printf("%2d. %s\n", i+1, roomInfo.Room.Name)
		fmt.Printf("    Reason: %s\n", roomInfo.Reason)
	}
	fmt.Printf("\nRooms to keep: %d\n", len(roomsToKeep))

	if !skipConfirm {
		fmt.Print("\nAre you sure you want to leave these rooms? This cannot be undone. (y/n): ")
		var confirmation string
		if _, err := fmt.Scanln(&confirmation); err != nil {
			return fmt.Errorf("failed to read confirmation: %w", err)
		}
		confirmation = strings.ToLower(strings.TrimSpace(confirmation))

		if confirmation != "y" && confirmation != "yes" {
			fmt.Println("Operation cancelled by user.")
			return nil
		}
	}

	fmt.Println("\nLeaving rooms...")
	leftCount, err := c.actions.LeaveRooms(ctx, roomsToLeave)
	if err != nil {
		return fmt.Errorf("error during room leaving process: %w", err)
	}

	fmt.Printf("\nResults:\n")
	fmt.Printf("- Rooms left (inactive for %d days): %d of %d\n", days, leftCount, len(roomsToLeave))
	fmt.Printf("- Rooms kept: %d\n", len(roomsToKeep))

	c.log.Info("Leave inactive rooms action completed successfully")
	return nil
}
