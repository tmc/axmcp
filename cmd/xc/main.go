package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/macgo"
	"github.com/tmc/axmcp/internal/crash"
	"github.com/tmc/axmcp/internal/devicectl"
	"github.com/tmc/axmcp/internal/project"
	"github.com/tmc/axmcp/internal/purego/coresim"
	"github.com/tmc/axmcp/internal/screen"
	"github.com/tmc/axmcp/internal/simctl"
	"github.com/tmc/axmcp/internal/ui"
	"github.com/tmc/axmcp/internal/xcodebuild"
)

func main() {
	runtime.LockOSThread()

	// Handle -h/--help before macgo.Start to avoid app bundle relaunch.
	for _, arg := range os.Args[1:] {
		if arg == "-h" || arg == "--help" || arg == "-help" || arg == "help" {
			_ = rootCmd.Help()
			os.Exit(0)
		}
	}

	// Initialize macgo for TCC identity
	cfg := macgo.NewConfig().
		WithAppName("xc").
		WithPermissions(macgo.Accessibility).
		WithAdHocSign()
	cfg.BundleID = "dev.tmc.xc"

	if err := macgo.Start(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "macgo start failed: %v\n", err)
		os.Exit(1)
	}

	// Initialize AppKit
	_ = appkit.GetNSApplicationClass().SharedApplication()

	ui.CheckTrust()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "xc",
	Short: "Xcode development CLI tool",
	Long:  `xc is a CLI tool for managing Xcode projects, simulators, and UI automation, powered by xcmcp libraries.`,
}

func init() {
	rootCmd.PersistentFlags().String("udid", "", "Target Simulator UDID")

	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(testCmd)
	rootCmd.AddCommand(simsCmd)
	rootCmd.AddCommand(projectCmd)
	rootCmd.AddCommand(uiCmd)
	rootCmd.AddCommand(iosCmd)

	simsCmd.AddCommand(simsListCmd)
	simsCmd.AddCommand(simsBootCmd)
	simsCmd.AddCommand(simsShutdownCmd)

	rootCmd.AddCommand(deviceCmd)
	rootCmd.AddCommand(appCmd)
	rootCmd.AddCommand(screenCmd)
	rootCmd.AddCommand(crashCmd)

	uiCmd.AddCommand(uiTreeCmd)
	uiCmd.AddCommand(uiTapCmd)
	uiCmd.AddCommand(uiInspectCmd)
	uiCmd.AddCommand(uiQueryCmd)
	uiCmd.AddCommand(uiScreenshotCmd)
	uiCmd.AddCommand(uiWaitCmd)

	crashCmd.AddCommand(crashListCmd)
	crashCmd.AddCommand(crashReadCmd)

	// Build & Test flags
	buildCmd.Flags().String("scheme", "", "Scheme to build")
	buildCmd.Flags().String("destination", "", "Destination specifier")
	testCmd.Flags().String("scheme", "", "Scheme to test")
	testCmd.Flags().String("destination", "", "Destination specifier")

	// UI Flags
	uiTapCmd.Flags().String("id", "", "Accessibility Identifier")
	uiTapCmd.Flags().Float64("x", 0, "X coordinate")
	uiTapCmd.Flags().Float64("y", 0, "Y coordinate")
	uiTapCmd.Flags().Int("pid", 0, "Target Process ID (iOS only)")
	uiInspectCmd.Flags().String("bundle-id", "", "Target application Bundle ID")
	uiTreeCmd.Flags().String("bundle-id", "", "Target application Bundle ID")
	uiScreenshotCmd.Flags().String("bundle-id", "", "Target application Bundle ID")
	uiScreenshotCmd.Flags().String("id", "", "Accessibility Identifier")
	uiScreenshotCmd.Flags().String("output", "screenshot.png", "Output filename")

	// Query Flags
	uiWaitCmd.Flags().String("id", "", "Accessibility Identifier to wait for")
	uiWaitCmd.Flags().Float64("timeout", 5.0, "Timeout in seconds")

	uiQueryCmd.Flags().String("bundle-id", "", "Target application Bundle ID")
	uiQueryCmd.Flags().String("role", "", "Filter by AXRole (e.g. AXButton)")
	uiQueryCmd.Flags().String("title", "", "Filter by Title (contains)")
	uiQueryCmd.Flags().String("label", "", "Filter by Label (contains)")
	uiQueryCmd.Flags().String("id", "", "Filter by Accessibility Identifier (exact)")
	uiQueryCmd.Flags().Bool("count", false, "Only print count of matches")

	// App Flags
	appListCmd.Flags().Bool("all", false, "Show system applications (com.apple.*)")
	appRunningCmd.Flags().Bool("all", false, "Show system applications (com.apple.*)")
	appLogsCmd.Flags().String("duration", "5m", "Lookback duration (e.g. 1h, 15m)")

	// Crash Flags
	crashListCmd.Flags().String("query", "", "Filter by process name")
	crashListCmd.Flags().Int("limit", 10, "Limit results")
	crashListCmd.Flags().String("after", "", "Show crashes after duration (e.g. 24h)")
}

// Helper to get UDID from flag or default to "booted"
func getUDID(cmd *cobra.Command) string {
	udid, _ := cmd.Flags().GetString("udid")
	if udid == "" {
		return "booted"
	}
	return udid
}

// ... (omitted sections)

// -- Build --

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build current scheme",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		scheme, _ := cmd.Flags().GetString("scheme")
		dest, _ := cmd.Flags().GetString("destination")

		if scheme == "" {
			fmt.Println("Error: --scheme is required")
			os.Exit(1)
		}

		res, err := xcodebuild.Build(ctx, xcodebuild.BuildOptions{
			Scheme:      scheme,
			Destination: dest,
		})
		if err != nil {
			fmt.Printf("Build failed: %v\n", err)
			os.Exit(1)
		}
		printBuildResult(res)
	},
}

// -- Test --

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Test current scheme",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		scheme, _ := cmd.Flags().GetString("scheme")
		dest, _ := cmd.Flags().GetString("destination")

		if scheme == "" {
			fmt.Println("Error: --scheme is required")
			os.Exit(1)
		}

		res, err := xcodebuild.Test(ctx, xcodebuild.BuildOptions{
			Scheme:      scheme,
			Destination: dest,
		})
		if err != nil {
			fmt.Printf("Test failed: %v\n", err)
			os.Exit(1)
		}
		printBuildResult(res)
	},
}

func printBuildResult(res *xcodebuild.BuildResult) {
	if res.Success {
		fmt.Println("✅ Succeeded")
	} else {
		fmt.Println("❌ Failed")
	}
	fmt.Printf("Duration: %d ms\n", res.Duration.Milliseconds())
	if len(res.Errors) > 0 {
		fmt.Println("\nErrors:")
		for _, e := range res.Errors {
			fmt.Printf("- %s\n", e)
		}
	}
}

// -- Sims --

var simsCmd = &cobra.Command{
	Use:   "sims",
	Short: "Manage simulators",
}

var simsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List simulators (default)",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		sims, err := simctl.List(ctx)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		for _, s := range sims {
			status := "🔴"
			if s.State == "Booted" {
				status = "🟢"
			} else if !s.IsAvailable {
				status = "⚪️"
			}
			fmt.Printf("%s %-40s %s (%s)\n", status, s.Name, s.UDID, s.Runtime)
		}
	},
}

var simsBootCmd = &cobra.Command{
	Use:   "boot [udid]",
	Short: "Boot a simulator",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := simctl.Boot(context.Background(), args[0]); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Booted.")
	},
}

var simsShutdownCmd = &cobra.Command{
	Use:   "shutdown [udid]",
	Short: "Shutdown a simulator",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := simctl.Shutdown(context.Background(), args[0]); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Shutdown.")
	},
}

// -- Project --

var projectCmd = &cobra.Command{
	Use:   "project [path]",
	Short: "Discover project info",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		path := "."
		if len(args) > 0 {
			path = args[0]
		}
		projs, err := project.Discover(path)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		for _, p := range projs {
			fmt.Printf("Project: %s (%s)\n", p.Name, p.Path)
			schemes, _ := p.GetSchemes(context.Background())
			fmt.Println("  Schemes:")
			for _, s := range schemes {
				fmt.Printf("    - %s\n", s)
			}
		}
	},
}

// -- Device --

var deviceCmd = &cobra.Command{
	Use:   "device",
	Short: "Control device hardware",
}

var deviceOrientationCmd = &cobra.Command{
	Use:   "orientation [portrait|landscape|...]",
	Short: "Get or set device orientation",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			val, err := simctl.GetOrientation(context.Background(), "booted")
			if err != nil {
				fmt.Printf("Error getting orientation: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Current orientation: %s\n", val)
			return
		}

		val := 1 // Portrait
		switch args[0] {
		case "portrait":
			val = 1
		case "landscape":
			val = 3 // LandscapeLeft
		case "landscape-left":
			val = 3
		case "landscape-right":
			val = 4
		case "upside-down":
			val = 2
		default:
			fmt.Println("Unknown orientation. Use portrait, landscape, landscape-left, landscape-right, or upside-down.")
			os.Exit(1)
		}
		ui.SharedDevice().SetOrientation(val)
		fmt.Printf("Set orientation to %s\n", args[0])
	},
}

var deviceAppearanceCmd = &cobra.Command{
	Use:   "appearance [light|dark]",
	Short: "Set device appearance",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		mode := args[0]
		if mode != "light" && mode != "dark" {
			fmt.Println("Use 'light' or 'dark'")
			os.Exit(1)
		}
		if err := simctl.SetAppearance(context.Background(), "booted", mode); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Set appearance to %s\n", mode)
	},
}

var deviceHomeCmd = &cobra.Command{
	Use:   "home",
	Short: "Press Home button",
	Run:   func(cmd *cobra.Command, args []string) { ui.SharedDevice().PressHome() },
}

var deviceVolumeUpCmd = &cobra.Command{
	Use:   "volume-up",
	Short: "Press Volume Up",
	Run:   func(cmd *cobra.Command, args []string) { ui.SharedDevice().PressVolumeUp() },
}

var deviceVolumeDownCmd = &cobra.Command{
	Use:   "volume-down",
	Short: "Press Volume Down",
	Run:   func(cmd *cobra.Command, args []string) { ui.SharedDevice().PressVolumeDown() },
}

var deviceLockCmd = &cobra.Command{
	Use:   "lock",
	Short: "Press Lock/Side button",
	Run:   func(cmd *cobra.Command, args []string) { ui.SharedDevice().PressLock() },
}

// -- New Device Commands --

// xc device privacy <action> <service> <bundleID>
var devicePrivacyCmd = &cobra.Command{
	Use:   "privacy [action] [service] [bundleID]",
	Short: "Manage privacy permissions",
	Long: `Manage privacy permissions for a bundle identifier.
Actions: grant, revoke, reset
Services: all, calendar, contacts, location, location-always, photos, microphone, camera, etc.`,
	Args: cobra.RangeArgs(2, 3),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		action := args[0]
		service := args[1]
		bid := ""
		if len(args) > 2 {
			bid = args[2]
		}
		udid := getUDID(cmd)

		if err := simctl.SetPrivacy(ctx, udid, action, service, bid); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Privacy %s %s %s\n", action, service, bid)
	},
}

// xc device location <lat> <lon>
var deviceLocationCmd = &cobra.Command{
	Use:   "location [lat] [lon]",
	Short: "Set device location",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		var lat, lon float64
		fmt.Sscanf(args[0], "%f", &lat)
		fmt.Sscanf(args[1], "%f", &lon)
		udid := getUDID(cmd)

		if err := simctl.SetLocation(ctx, udid, lat, lon); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Location set to %f, %f\n", lat, lon)
	},
}

// xc device open-url <url>
var deviceOpenURLCmd = &cobra.Command{
	Use:   "open-url [url]",
	Short: "Open a URL on the device",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		udid := getUDID(cmd)
		if err := simctl.OpenURL(ctx, udid, args[0]); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Opened URL: %s\n", args[0])
	},
}

// xc device add-media <path>
var deviceAddMediaCmd = &cobra.Command{
	Use:   "add-media [path]",
	Short: "Add photo/video to device library",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		udid := getUDID(cmd)
		path := args[0]
		if err := simctl.AddMedia(ctx, udid, path); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Added media: %s\n", path)
	},
}

func init() {
	deviceCmd.AddCommand(deviceHomeCmd)
	deviceCmd.AddCommand(deviceVolumeUpCmd)
	deviceCmd.AddCommand(deviceVolumeDownCmd)
	deviceCmd.AddCommand(deviceLockCmd)
	deviceCmd.AddCommand(deviceOrientationCmd)
	deviceCmd.AddCommand(deviceAppearanceCmd)

	// New commands
	deviceCmd.AddCommand(devicePrivacyCmd)
	deviceCmd.AddCommand(deviceLocationCmd)
	deviceCmd.AddCommand(deviceOpenURLCmd)
	deviceCmd.AddCommand(deviceAddMediaCmd)
}

// -- App --

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Control application lifecycle",
}

var appLaunchCmd = &cobra.Command{
	Use:   "launch [bundleID]",
	Short: "Launch an application",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		bid := ""
		if len(args) > 0 {
			bid = args[0]
		}

		udid := getUDID(cmd)
		udidChanged := cmd.Flags().Changed("udid") || cmd.InheritedFlags().Changed("udid")

		if udidChanged {
			fmt.Printf("Launching %s on device %s...\n", bid, udid)
			ctx := context.Background()

			// iOS Launch via simctl
			if err := simctl.LaunchApp(ctx, udid, bid, nil); err != nil {
				fmt.Printf("Error launching app: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Launched %s\n", bid)
		} else {
			// macOS Launch
			ui.NewApp(bid).Launch()
			fmt.Printf("Launched %s\n", bid)
		}
	},
}

var appTerminateCmd = &cobra.Command{
	Use:   "terminate [bundleID]",
	Short: "Terminate an application",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		bid := ""
		if len(args) > 0 {
			bid = args[0]
		}

		udid := getUDID(cmd)
		udidChanged := cmd.Flags().Changed("udid") || cmd.InheritedFlags().Changed("udid")

		if udidChanged {
			ctx := context.Background()
			if err := simctl.Terminate(ctx, udid, bid); err != nil {
				fmt.Printf("Error terminating app: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Terminated %s on device %s\n", bid, udid)
		} else {
			ui.NewApp(bid).Terminate()
			fmt.Printf("Terminated %s\n", bid)
		}
	},
}

var appInstallCmd = &cobra.Command{
	Use:   "install [path]",
	Short: "Install an application (.app)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		path := args[0]
		udid := getUDID(cmd)
		udidChanged := cmd.Flags().Changed("udid") || cmd.InheritedFlags().Changed("udid")

		if udidChanged {
			if err := simctl.InstallApp(context.Background(), udid, path); err != nil {
				fmt.Printf("Error installing app: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Installed %s on device %s\n", path, udid)
		} else {
			fmt.Println("Error: 'install' for macOS is not supported. Use --udid to install on Simulator.")
			os.Exit(1)
		}
	},
}

var appUninstallCmd = &cobra.Command{
	Use:   "uninstall [bundleID]",
	Short: "Uninstall an application",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		bid := args[0]
		udid := getUDID(cmd)
		udidChanged := cmd.Flags().Changed("udid") || cmd.InheritedFlags().Changed("udid")

		if udidChanged {
			if err := simctl.UninstallApp(context.Background(), udid, bid); err != nil {
				fmt.Printf("Error uninstalling app: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Uninstalled %s from device %s\n", bid, udid)
		} else {
			fmt.Println("Error: 'uninstall' for macOS is not supported. Use --udid to uninstall from Simulator.")
			os.Exit(1)
		}
	},
}

var appLogsCmd = &cobra.Command{
	Use:   "logs [bundleID|processName]",
	Short: "Get application logs",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		query := args[0]
		duration, _ := cmd.Flags().GetString("duration")

		logs, err := simctl.GetAppLogs(context.Background(), getUDID(cmd), query, duration)
		if err != nil {
			fmt.Printf("Error getting logs: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(logs)
	},
}

var appListCmd = &cobra.Command{
	Use:   "list [udid]",
	Short: "List installed applications",
	Run: func(cmd *cobra.Command, args []string) {
		udid := getUDID(cmd)
		if len(args) > 0 {
			udid = args[0]
		}
		showAll, _ := cmd.Flags().GetBool("all")

		out, err := simctl.ListApps(context.Background(), udid)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		lines := strings.Split(out, "\n")
		for _, line := range lines {
			if !showAll && strings.Contains(line, "com.apple.") {
				continue
			}
			if strings.TrimSpace(line) == "" {
				continue
			}
			fmt.Println(line)
		}
	},
}

var appRunningCmd = &cobra.Command{
	Use:   "running [udid]",
	Short: "List running applications/services",
	Run: func(cmd *cobra.Command, args []string) {
		udid := getUDID(cmd)
		if len(args) > 0 {
			udid = args[0]
		}
		showAll, _ := cmd.Flags().GetBool("all")

		out, err := simctl.ListRunningApps(context.Background(), udid)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		// out is already []string
		lines := out
		for _, line := range lines {
			if !showAll && strings.Contains(line, "com.apple.") {
				continue
			}
			if strings.TrimSpace(line) == "" {
				continue
			}
			fmt.Println(line)
		}
	},
}

// xc app container <bundleID>
var appContainerCmd = &cobra.Command{
	Use:   "container [bundleID]",
	Short: "Get app container path",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		udid := getUDID(cmd)
		bid := args[0]

		path, err := simctl.GetAppContainer(ctx, udid, bid, "data")
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(path)
	},
}

func init() {
	appCmd.AddCommand(appLaunchCmd)
	appCmd.AddCommand(appTerminateCmd)
	appCmd.AddCommand(appListCmd)
	appCmd.AddCommand(appRunningCmd)
	appCmd.AddCommand(appInstallCmd)
	appCmd.AddCommand(appUninstallCmd)
	appCmd.AddCommand(appLogsCmd)
	appCmd.AddCommand(appContainerCmd)
}

// -- Screen --

var screenCmd = &cobra.Command{
	Use:   "screen",
	Short: "Screen capture and recording",
}

var screenShotCmd = &cobra.Command{
	Use:   "shot [filename]",
	Short: "Take a screenshot",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		filename := "screenshot.png"
		if len(args) > 0 {
			filename = args[0]
		}

		udid := getUDID(cmd)
		data, err := screen.CaptureSimulator(context.Background(), udid)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		if filename == "-" {
			os.Stdout.Write(data)
		} else {
			if err := os.WriteFile(filename, data, 0644); err != nil {
				fmt.Printf("Failed to write file: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Screenshot saved to %s\n", filename)
		}
	},
}

func init() {
	screenCmd.AddCommand(screenShotCmd)
}

// -- UI --

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Interact with Simulator UI",
}

// ...

var uiTapCmd = &cobra.Command{
	Use:   "tap",
	Short: "Tap an element or coordinate",
	Example: `  xc ui tap --id "login_button"
  xc ui tap --udid booted --x 100 --y 200`,
	Run: func(cmd *cobra.Command, args []string) {
		id, _ := cmd.Flags().GetString("id")
		x, _ := cmd.Flags().GetFloat64("x")
		y, _ := cmd.Flags().GetFloat64("y")
		udid, _ := cmd.Flags().GetString("udid")
		if udid == "" {
			udid = getUDID(cmd)
		}

		pid, _ := cmd.Flags().GetInt("pid")
		udidChanged := cmd.Flags().Changed("udid") || cmd.InheritedFlags().Changed("udid") || pid != 0

		if udidChanged {
			// iOS Smart Tap
			if x == 0 && y == 0 && id == "" {
				fmt.Println("Error: Must specify --id or --x and --y")
				os.Exit(1)
			}

			device, err := getIOSDevice(udid)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}

			if id != "" {
				targetX, targetY, found := findElementCenterByID(device, id, pid)
				if !found {
					fmt.Printf("Element '%s' not found on device %s\n", id, device.Name())
					os.Exit(1)
				}
				x, y = targetX, targetY
				fmt.Printf("Resolved '%s' to (%.1f, %.1f)\n", id, x, y)
			}

			fmt.Printf("Tapping at %.1f, %.1f on device %s (%s)...\n", x, y, device.Name(), device.UDID())
			err = device.Tap(x, y)
			if err != nil {
				fmt.Printf("Tap failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Tap sent.")
			return
		}

		if x != 0 || y != 0 {
			fmt.Println("Error: coordinate taps require --udid")
			os.Exit(1)
		}
		if id != "" {
			el := ui.ElementByID(id)
			if el == nil {
				fmt.Printf("Element '%s' not found.\n", id)
				os.Exit(1)
			}
			el.Tap()
			fmt.Printf("Tapped element '%s'\n", id)
		} else {
			fmt.Println("Error: must specify --id")
			cmd.Usage()
		}
	},
}

var uiTreeCmd = &cobra.Command{
	Use:   "tree",
	Short: "Dump UI hierarchy",
	Example: `  xc ui tree --bundle-id com.apple.finder
  xc ui tree (dumps Simulator)`,
	Run: func(cmd *cobra.Command, args []string) {
		bid, _ := cmd.Flags().GetString("bundle-id")
		app := ui.Application()
		if bid != "" {
			app = ui.ApplicationWithBundleID(bid)
		}
		if !app.Exists() {
			target := bid
			if target == "" {
				target = "Simulator (Default)"
			}
			msg := fmt.Sprintf("Error: Application '%s' not found or not accessible.\nPlease check Accessibility Permissions for Terminal/xc.\n", target)
			fmt.Println(msg)
			return
		}
		el := app.Element()
		if el == nil {
			fmt.Println("Error: application element is not accessible.")
			return
		}
		fmt.Println(el.VisualTree())
	},
}

var uiInspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect element attributes (JSON)",
	Example: `  xc ui inspect --bundle-id com.apple.finder
  xc ui inspect "accessibility_id"`,
	Run: func(cmd *cobra.Command, args []string) {
		var el *ui.Element
		bid, _ := cmd.Flags().GetString("bundle-id")

		if bid != "" {
			app := ui.NewApp(bid)
			app.Activate()
			el = app.Element()
		} else if len(args) > 0 {
			el = ui.ElementByID(args[0])
		} else {
			el = ui.Application().Element()
		}
		if el == nil {
			fmt.Println("Element not found.")
			os.Exit(1)
		}

		attrs := el.Attributes()
		fmt.Printf("{\n")
		fmt.Printf("  \"label\": %q,\n", attrs.Label)
		fmt.Printf("  \"identifier\": %q,\n", attrs.Identifier)
		fmt.Printf("  \"title\": %q,\n", attrs.Title)
		fmt.Printf("  \"value\": %q,\n", attrs.Value)
		fmt.Printf("  \"frame\": {\"x\": %f, \"y\": %f, \"w\": %f, \"h\": %f},\n", attrs.Frame.Origin.X, attrs.Frame.Origin.Y, attrs.Frame.Size.Width, attrs.Frame.Size.Height)
		fmt.Printf("  \"enabled\": %v,\n", attrs.Enabled)
		fmt.Printf("  \"selected\": %v,\n", attrs.Selected)
		fmt.Printf("  \"has_focus\": %v\n", attrs.HasFocus)
		fmt.Printf("}\n")
	},
}

var uiQueryCmd = &cobra.Command{
	Use:   "query",
	Short: "Query UI elements",
	Long: `Query UI elements with various filters.
Filters matches using AND logic (all conditions must match).
Supports searching by Role, Title (contains), Label (contains), and Accessibility Identifier (exact).`,
	Example: `  xc ui query --bundle-id com.apple.finder --role AXWindow
  xc ui query --bundle-id com.apple.dt.Xcode --title "Welcome"
  xc ui query --id "submit_button" --count
  xc ui query --role AXButton --label "Save"`,
	Run: func(cmd *cobra.Command, args []string) {
		bid, _ := cmd.Flags().GetString("bundle-id")
		role, _ := cmd.Flags().GetString("role")
		title, _ := cmd.Flags().GetString("title")
		label, _ := cmd.Flags().GetString("label")
		id, _ := cmd.Flags().GetString("id")
		countOnly, _ := cmd.Flags().GetBool("count")

		var app *ui.App
		if bid != "" {
			app = ui.ApplicationWithBundleID(bid)
		} else {
			app = ui.Application()
		}

		if !app.Exists() {
			fmt.Println("Application not found or not running.")
			os.Exit(1)
		}

		query := ui.QueryParams{
			Role:       role,
			Title:      title,
			Label:      label,
			Identifier: id,
		}

		matches := app.Element().Query(query)

		if countOnly {
			fmt.Printf("%d\n", len(matches))
			return
		}

		for _, m := range matches {
			attrs := m.Attributes()
			var idStr, labelStr, titleStr string
			if attrs.Identifier != "" {
				idStr = fmt.Sprintf("id=%q ", attrs.Identifier)
			}
			if attrs.Label != "" {
				labelStr = fmt.Sprintf("label=%q ", attrs.Label)
			}
			if attrs.Title != "" {
				titleStr = fmt.Sprintf("title=%q ", attrs.Title)
			}
			fmt.Printf("- %s: %s%s%s\n", m.Role(), idStr, labelStr, titleStr)
		}
	},
}

var uiScreenshotCmd = &cobra.Command{
	Use:   "screenshot",
	Short: "Take a screenshot of an element",
	Example: `  xc ui screenshot --id "login_button"
  xc ui screenshot --bundle-id com.apple.finder --output finder.png`,
	Run: func(cmd *cobra.Command, args []string) {
		bid, _ := cmd.Flags().GetString("bundle-id")
		id, _ := cmd.Flags().GetString("id")
		output, _ := cmd.Flags().GetString("output")

		var el *ui.Element

		if bid != "" {
			app := ui.ApplicationWithBundleID(bid)
			if !app.Exists() {
				fmt.Println("Application not found or not running.")
				os.Exit(1)
			}
			if id != "" {
				matches := app.Element().Query(ui.QueryParams{Identifier: id})
				if len(matches) > 0 {
					el = matches[0]
				} else {
					fmt.Printf("Element with id '%s' not found in app '%s'\n", id, bid)
					os.Exit(1)
				}
			} else {
				el = app.Element()
				attr := el.Attributes()
				if attr.Frame.Size.Width == 0 && attr.Frame.Size.Height == 0 {
					windows := el.Windows()
					if len(windows) > 0 {
						fmt.Printf("Using first window: '%s'\n", windows[0].Title())
						el = windows[0]
					}
				}
			}
		} else if id != "" {
			el = ui.ElementByID(id)
		} else {
			el = ui.Application().Element()
		}

		if el == nil {
			fmt.Println("Element not found.")
			os.Exit(1)
		}

		data, err := el.Screenshot()
		if err != nil {
			fmt.Printf("Screenshot failed: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(output, data, 0644); err != nil {
			fmt.Printf("Failed to write to %s: %v\n", output, err)
			os.Exit(1)
		}
		fmt.Printf("Screenshot saved to %s\n", output)
	},
}

var uiWaitCmd = &cobra.Command{
	Use:   "wait",
	Short: "Wait for an element to exist",
	Example: `  xc ui wait --id "loaded_content"
  xc ui wait --id "login_button" --timeout 10`,
	Run: func(cmd *cobra.Command, args []string) {
		id, _ := cmd.Flags().GetString("id")
		timeout, _ := cmd.Flags().GetFloat64("timeout")

		if id == "" {
			fmt.Println("Error: --id is required")
			os.Exit(1)
		}

		if timeout == 0 {
			timeout = 5.0
		}

		start := time.Now()
		found := false
		for time.Since(start).Seconds() < timeout {
			el := ui.ElementByID(id)
			if el != nil && el.Exists() {
				found = true
				break
			}
			time.Sleep(200 * time.Millisecond)
		}

		if found {
			fmt.Printf("Element '%s' found.\n", id)
		} else {
			fmt.Printf("Element '%s' not found after %.1fs.\n", id, timeout)
			os.Exit(1)
		}
	},
}

// -- Crash --

var crashCmd = &cobra.Command{
	Use:   "crash",
	Short: "Diagnostics and crash reporting",
}

var crashListCmd = &cobra.Command{
	Use:   "list",
	Short: "List crash reports",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		query, _ := cmd.Flags().GetString("query")
		limit, _ := cmd.Flags().GetInt("limit")
		afterStr, _ := cmd.Flags().GetString("after")

		var after time.Time
		if afterStr != "" {
			d, err := time.ParseDuration(afterStr)
			if err == nil {
				after = time.Now().Add(-d)
			} else {
				fmt.Printf("Invalid duration: %v\n", err)
				os.Exit(1)
			}
		}

		opts := crash.ListOptions{
			Query: query,
			Limit: limit,
			After: after,
		}

		reports, err := crash.List(ctx, opts)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		for _, r := range reports {
			fmt.Printf("%s\t%s\t%s\n", r.ModTime.Format(time.Kitchen), r.Process, r.Name)
		}
	},
}

var crashReadCmd = &cobra.Command{
	Use:   "read [path]",
	Short: "Read a crash report",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		content, err := crash.Read(ctx, args[0])
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(content)
	},
}

// -- iOS (CoreSimulator direct access) --

var iosCmd = &cobra.Command{
	Use:   "ios",
	Short: "Query iOS app content directly via CoreSimulator",
	Long: `Query actual iOS app accessibility tree from booted simulators.
Unlike 'ui' commands which query the Simulator.app macOS window,
'ios' commands query the actual iOS app content running inside the simulator.`,
}

var iosTreeCmd = &cobra.Command{
	Use:   "tree",
	Short: "Get iOS accessibility tree from booted simulator",
	Example: `  xc ios tree
  xc ios tree --udid 3E91FD96-BAF0-461E-8D77-9ACBAA8A0527`,
	Run: func(cmd *cobra.Command, args []string) {
		if !coresim.Available() {
			fmt.Println("Error: CoreSimulator.framework not available")
			os.Exit(1)
		}

		udid, _ := cmd.Flags().GetString("udid")
		device, err := getIOSDevice(udid)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		pid, _ := cmd.Flags().GetInt("pid")

		fmt.Printf("Querying iOS accessibility for: %s (%s)\n", device.Name(), device.UDID())

		var elements []*coresim.AccessibilityElement
		if pid != 0 {
			elements, err = device.GetAccessibilityElementsForPID(pid)
		} else {
			elements, err = device.GetAccessibilityElements()
		}

		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		jsonData, _ := json.MarshalIndent(elements, "", "  ")
		fmt.Println(string(jsonData))
	},
}

var iosHitTestCmd = &cobra.Command{
	Use:   "hit-test",
	Short: "Get iOS element at screen coordinates",
	Example: `  xc ios hit-test --x 200 --y 400
  xc ios hit-test --x 100 --y 300 --udid 3E91FD96-BAF0-461E-8D77-9ACBAA8A0527`,
	Run: func(cmd *cobra.Command, args []string) {
		if !coresim.Available() {
			fmt.Println("Error: CoreSimulator.framework not available")
			os.Exit(1)
		}

		udid, _ := cmd.Flags().GetString("udid")
		x, _ := cmd.Flags().GetFloat64("x")
		y, _ := cmd.Flags().GetFloat64("y")

		if x == 0 && y == 0 {
			x, y = 200, 400 // Default to center-ish
		}

		device, err := getIOSDevice(udid)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Hit testing at (%.0f, %.0f) on: %s\n\n", x, y, device.Name())

		element, err := device.GetAccessibilityElementAtPoint(x, y, "")
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		if element == nil {
			fmt.Println("No element found at that position")
			return
		}

		jsonData, _ := json.MarshalIndent(map[string]interface{}{
			"point":   map[string]float64{"x": x, "y": y},
			"element": element,
		}, "", "  ")
		fmt.Println(string(jsonData))
	},
}

var iosSimulatorsCmd = &cobra.Command{
	Use:   "simulators",
	Short: "List iOS simulators via CoreSimulator",
	Example: `  xc ios simulators
  xc ios simulators --state booted`,
	Run: func(cmd *cobra.Command, args []string) {
		if !coresim.Available() {
			fmt.Println("Error: CoreSimulator.framework not available")
			os.Exit(1)
		}

		state, _ := cmd.Flags().GetString("state")

		set, err := coresim.DefaultSet()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		devices := set.Devices()
		var result []map[string]interface{}

		for _, dev := range devices {
			devState := dev.State()
			stateStr := devState.String()

			if state != "" {
				if state == "booted" && devState != coresim.SimDeviceStateBooted {
					continue
				}
				if state == "shutdown" && devState != coresim.SimDeviceStateShutdown {
					continue
				}
			}

			result = append(result, map[string]interface{}{
				"udid":       dev.UDID(),
				"name":       dev.Name(),
				"state":      stateStr,
				"deviceType": dev.DeviceTypeIdentifier(),
				"runtimeId":  dev.RuntimeIdentifier(),
			})
		}

		fmt.Printf("Found %d simulators:\n\n", len(result))

		for _, dev := range result {
			status := "🔴"
			if dev["state"] == "Booted" {
				status = "🟢"
			}
			fmt.Printf("%s %-40s %s\n", status, dev["name"], dev["udid"])
		}
	},
}

var iosDeviceInfoCmd = &cobra.Command{
	Use:   "device-info",
	Short: "Get detailed info about a simulator",
	Example: `  xc ios device-info
  xc ios device-info --udid 3E91FD96-BAF0-461E-8D77-9ACBAA8A0527`,
	Run: func(cmd *cobra.Command, args []string) {
		if !coresim.Available() {
			fmt.Println("Error: CoreSimulator.framework not available")
			os.Exit(1)
		}

		udid, _ := cmd.Flags().GetString("udid")
		device, err := getIOSDevice(udid)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		info := map[string]interface{}{
			"udid":       device.UDID(),
			"name":       device.Name(),
			"state":      device.State().String(),
			"deviceType": device.DeviceTypeIdentifier(),
			"runtimeId":  device.RuntimeIdentifier(),
		}

		jsonData, _ := json.MarshalIndent(info, "", "  ")
		fmt.Println(string(jsonData))
	},
}

func init() {
	// iOS command group
	iosCmd.AddCommand(iosTreeCmd)
	iosCmd.AddCommand(iosHitTestCmd)
	iosCmd.AddCommand(iosSimulatorsCmd)
	iosCmd.AddCommand(iosDeviceInfoCmd)

	// iOS flags
	iosTreeCmd.Flags().String("udid", "", "Simulator UDID (uses first booted if not specified)")
	iosTreeCmd.Flags().Int("pid", 0, "Target Process ID (skips HitTest)")
	iosHitTestCmd.Flags().String("udid", "", "Simulator UDID (uses first booted if not specified)")
	iosHitTestCmd.Flags().Float64("x", 0, "X coordinate in screen points")
	iosHitTestCmd.Flags().Float64("y", 0, "Y coordinate in screen points")
	iosSimulatorsCmd.Flags().String("state", "", "Filter by state: 'booted', 'shutdown', or empty for all")
	iosDeviceInfoCmd.Flags().String("udid", "", "Simulator UDID (uses first booted if not specified)")
}

// getIOSDevice returns a simulator device by UDID, or the first booted device if no UDID specified.
func getIOSDevice(udid string) (coresim.SimDevice, error) {
	if udid != "" && udid != "booted" {
		device, found := coresim.FindDeviceByUDID(udid)
		if !found {
			return coresim.SimDevice{}, fmt.Errorf("simulator with UDID %s not found", udid)
		}
		return device, nil
	}

	// Use first booted device
	booted := coresim.ListBootedDevices()
	if len(booted) == 0 {
		return coresim.SimDevice{}, fmt.Errorf("no booted simulators found")
	}
	return booted[0], nil
}

func findElementCenterByID(device coresim.SimDevice, id string, pid int) (float64, float64, bool) {
	var roots []*coresim.AccessibilityElement
	var err error

	if pid != 0 {
		roots, err = device.GetAccessibilityElementsForPID(pid)
	} else {
		roots, err = device.GetAccessibilityElements()
	}

	if err != nil {
		fmt.Printf("Error fetching tree: %v\n", err)
		return 0, 0, false
	}

	var found *coresim.AccessibilityElement
	var search func([]*coresim.AccessibilityElement)
	search = func(elements []*coresim.AccessibilityElement) {
		if found != nil {
			return
		}
		for _, el := range elements {
			if el.AXIdentifier == id || el.AXLabel == id {
				found = el
				return
			}
			if len(el.AXChildren) > 0 {
				search(el.AXChildren)
			}
		}
	}
	search(roots)

	if found != nil {
		x := found.AXFrame["x"]
		y := found.AXFrame["y"]
		w := found.AXFrame["w"]
		h := found.AXFrame["h"]
		if w > 0 && h > 0 {
			return x + w/2, y + h/2, true
		}
		// Fallback if frame invalid?
		fmt.Printf("Warning: Element '%s' found but has invalid frame %v\n", id, found.AXFrame)
	}
	return 0, 0, false
}

// -- Physical Device (via devicectl) --

var physicalCmd = &cobra.Command{
	Use:   "physical",
	Short: "Manage physical iOS/macOS devices",
	Long:  `Commands for interacting with physical devices connected via USB or network.`,
}

var physicalListCmd = &cobra.Command{
	Use:   "list",
	Short: "List connected physical devices",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		devices, err := devicectl.ListDevices(ctx)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		if len(devices) == 0 {
			fmt.Println("No physical devices found.")
			return
		}

		fmt.Printf("Found %d device(s):\n\n", len(devices))
		for _, d := range devices {
			conn := d.ConnectionType
			if conn == "" {
				conn = "unknown"
			}
			fmt.Printf("%-40s %s (%s %s, %s)\n", d.Name, d.Identifier, d.Platform, d.OSVersion, conn)
		}
	},
}

var physicalInfoCmd = &cobra.Command{
	Use:   "info [identifier]",
	Short: "Get detailed info about a physical device",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		info, err := devicectl.DeviceInfo(ctx, args[0])
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		jsonData, _ := json.MarshalIndent(info, "", "  ")
		fmt.Println(string(jsonData))
	},
}

var physicalInstallCmd = &cobra.Command{
	Use:   "install [identifier] [app-path]",
	Short: "Install an app (.app or .ipa) on a physical device",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		if err := devicectl.InstallApp(ctx, args[0], args[1]); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("App installed successfully.")
	},
}

var physicalUninstallCmd = &cobra.Command{
	Use:   "uninstall [identifier] [bundle-id]",
	Short: "Uninstall an app from a physical device",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		if err := devicectl.UninstallApp(ctx, args[0], args[1]); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("App uninstalled successfully.")
	},
}

var physicalLaunchCmd = &cobra.Command{
	Use:   "launch [identifier] [bundle-id]",
	Short: "Launch an app on a physical device",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		if err := devicectl.LaunchApp(ctx, args[0], args[1]); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("App launched successfully.")
	},
}

var physicalTerminateCmd = &cobra.Command{
	Use:   "terminate [identifier] [bundle-id]",
	Short: "Terminate an app on a physical device",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		if err := devicectl.TerminateApp(ctx, args[0], args[1]); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("App terminated successfully.")
	},
}

var physicalRebootCmd = &cobra.Command{
	Use:   "reboot [identifier]",
	Short: "Reboot a physical device",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		if err := devicectl.RebootDevice(ctx, args[0]); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Device rebooting...")
	},
}

func init() {
	rootCmd.AddCommand(physicalCmd)

	physicalCmd.AddCommand(physicalListCmd)
	physicalCmd.AddCommand(physicalInfoCmd)
	physicalCmd.AddCommand(physicalInstallCmd)
	physicalCmd.AddCommand(physicalUninstallCmd)
	physicalCmd.AddCommand(physicalLaunchCmd)
	physicalCmd.AddCommand(physicalTerminateCmd)
	physicalCmd.AddCommand(physicalRebootCmd)
}

// -- Xcode --

var xcodeCmd = &cobra.Command{
	Use:   "xcode",
	Short: "Drive Xcode via accessibility automation",
}

var xcodeAddTargetCmd = &cobra.Command{
	Use:   "add-target",
	Short: "Add a new target via File > New > Target wizard",
	Example: `  xc xcode add-target --template "Widget Extension" --product NanoclawWidget
  xc xcode add-target --template "App Intent Extension" --product NanoclawIntents --team "My Team"`,
	Run: func(cmd *cobra.Command, args []string) {
		template, _ := cmd.Flags().GetString("template")
		product, _ := cmd.Flags().GetString("product")
		bundleID, _ := cmd.Flags().GetString("bundle-id")
		team, _ := cmd.Flags().GetString("team")

		if template == "" {
			fmt.Fprintln(os.Stderr, "error: --template is required")
			os.Exit(1)
		}
		if product == "" {
			fmt.Fprintln(os.Stderr, "error: --product is required")
			os.Exit(1)
		}

		app, err := axuiautomation.NewApplication("com.apple.dt.Xcode")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: Xcode is not running: %v\n", err)
			os.Exit(1)
		}
		defer app.Close()

		if err := app.Activate(); err != nil {
			fmt.Fprintf(os.Stderr, "error: failed to activate Xcode: %v\n", err)
			os.Exit(1)
		}
		// Give Xcode time to come to front.
		time.Sleep(600 * time.Millisecond)

		// Check if the template chooser sheet is already open.
		sheet, _ := waitForSheet(app, 300*time.Millisecond)
		if sheet == nil {
			// Select the project root node so File > New > Target… is enabled.
			ensureXcodeProjectNodeSelected(app)
			time.Sleep(300 * time.Millisecond)

			// Try both Unicode ellipsis and ASCII variant of "Target".
			var menuErr error
			for _, name := range []string{"Target\u2026", "Target..."} {
				menuErr = app.ClickMenuItem([]string{"File", "New", name})
				if menuErr == nil {
					break
				}
			}
			if menuErr != nil {
				fmt.Fprintf(os.Stderr, "error: failed to open File > New > Target: %v\n", menuErr)
				os.Exit(1)
			}

			var err error
			sheet, err = waitForSheet(app, 8*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: template chooser did not appear: %v\n", err)
				os.Exit(1)
			}
		}

		_ = typeIntoSearchField(sheet, template)
		time.Sleep(800 * time.Millisecond)
		if err := clickTemplateByName(sheet, template); err != nil {
			axuiautomation.SendEscape()
			msg := fmt.Sprintf("error: failed to select template %q: %v", template, err)
			fmt.Fprintln(os.Stderr, msg)
			os.Exit(1)
		}

		if err := clickBtn(sheet, "Next", 3*time.Second); err != nil {
			axuiautomation.SendEscape()
			fmt.Fprintf(os.Stderr, "error: failed to click Next: %v\n", err)
			os.Exit(1)
		}
		time.Sleep(300 * time.Millisecond)

		sheet2, err := waitForSheet(app, 5*time.Second)
		if err != nil {
			sheet2 = app.Root()
		}

		if err := fillField(sheet2, "Product Name", product); err != nil {
			axuiautomation.SendEscape()
			fmt.Fprintf(os.Stderr, "error: failed to fill product name: %v\n", err)
			os.Exit(1)
		}
		if bundleID != "" {
			_ = fillField(sheet2, "Bundle Identifier", bundleID)
		}
		if team != "" {
			_ = selectPopup(sheet2, "team", team)
		}

		if err := clickBtn(sheet2, "Finish", 3*time.Second); err != nil {
			axuiautomation.SendEscape()
			fmt.Fprintf(os.Stderr, "error: failed to click Finish: %v\n", err)
			os.Exit(1)
		}

		time.Sleep(500 * time.Millisecond)
		dismissActivateScheme(app)

		fmt.Printf("added target %q (template: %s)\n", product, template)
	},
}

func init() {
	rootCmd.AddCommand(xcodeCmd)
	xcodeCmd.AddCommand(xcodeAddTargetCmd)

	xcodeAddTargetCmd.Flags().String("template", "", "Target template name (e.g. 'Widget Extension')")
	xcodeAddTargetCmd.Flags().String("product", "", "Product name for the new target")
	xcodeAddTargetCmd.Flags().String("bundle-id", "", "Bundle identifier (optional)")
	xcodeAddTargetCmd.Flags().String("team", "", "Development team name (optional)")

}

// helpers shared by xcodeAddTargetCmd — thin wrappers over axuiautomation.

func waitForSheet(app *axuiautomation.Application, timeout time.Duration) (*axuiautomation.Element, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if sheets := app.Sheets().AllElements(); len(sheets) > 0 {
			return sheets[0], nil
		}
		if dialogs := app.Dialogs().AllElements(); len(dialogs) > 0 {
			return dialogs[0], nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil, axuiautomation.ErrTimeout
}

func clickTemplateByName(sheet *axuiautomation.Element, name string) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var found *axuiautomation.Element

		sheet.Descendants().ForEach(func(e *axuiautomation.Element) bool {
			t, v := e.Title(), e.Value()
			if strings.Contains(t, name) || strings.Contains(v, name) {
				found = e
				return false
			}
			return true
		})
		if found != nil {
			_ = found.ScrollToVisible()
			err := found.Click()
			found.Release()
			return err
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("template %q not found", name)
}

func clickBtn(parent *axuiautomation.Element, title string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if btn := parent.Descendants().ByRole("AXButton").ByTitle(title).First(); btn != nil {
			if btn.IsEnabled() {
				err := btn.Click()
				btn.Release()
				return err
			}
			btn.Release()
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("button %q not found or not enabled", title)
}

func fillField(parent *axuiautomation.Element, label, text string) error {
	tf := parent.Descendants().ByRole("AXTextField").Matching(func(e *axuiautomation.Element) bool {
		return strings.EqualFold(e.Identifier(), label) ||
			strings.EqualFold(e.Title(), label)
	}).First()
	if tf == nil {
		// Fall back to the first non-search text field.
		tf = parent.Descendants().ByRole("AXTextField").Matching(func(e *axuiautomation.Element) bool {
			role := e.Role()
			return role != "AXSearchField"
		}).First()
	}
	if tf == nil {
		return fmt.Errorf("text field %q not found", label)
	}
	defer tf.Release()
	if err := tf.Focus(); err != nil {
		if err2 := tf.Click(); err2 != nil {
			return fmt.Errorf("focusing %q: %w", label, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = axuiautomation.SendKeyCombo(0x00, false, false, false, true) // Cmd+A
	time.Sleep(30 * time.Millisecond)
	return tf.TypeText(text)
}

func selectPopup(parent *axuiautomation.Element, labelHint, value string) error {
	popup := parent.Descendants().ByRole("AXPopUpButton").Matching(func(e *axuiautomation.Element) bool {
		return strings.Contains(strings.ToLower(e.Identifier()), labelHint) ||
			strings.Contains(strings.ToLower(e.Title()), labelHint)
	}).First()
	if popup == nil {
		return fmt.Errorf("popup %q not found", labelHint)
	}
	defer popup.Release()
	return popup.SelectMenuItem(value)
}

// typeIntoSearchField finds the search/filter text field in the template
// chooser sheet and types the template name into it.
func typeIntoSearchField(sheet *axuiautomation.Element, text string) error {
	// Retry finding the search field — it may take a moment to appear.
	var tf *axuiautomation.Element
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tf = sheet.Descendants().ByRole("AXSearchField").First()
		if tf != nil {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if tf == nil {
		return fmt.Errorf("search field not found")
	}
	defer tf.Release()
	if err := tf.Click(); err != nil {
		return fmt.Errorf("click search field: %w", err)
	}
	time.Sleep(150 * time.Millisecond)
	return tf.TypeText(text)
}

// ensureXcodeProjectNodeSelected clicks the root .xcodeproj/.xcworkspace node
// in the Project Navigator so that File > New > Target... is enabled.
func ensureXcodeProjectNodeSelected(app *axuiautomation.Application) {
	win := app.Windows().First()
	if win == nil {
		return
	}
	defer win.Release()
	outline := win.Descendants().ByRole("AXOutline").First()
	if outline == nil {
		return
	}
	defer outline.Release()
	row := outline.Descendants().ByRole("AXRow").Matching(func(e *axuiautomation.Element) bool {
		t := e.Title()
		return strings.HasSuffix(t, ".xcodeproj") || strings.HasSuffix(t, ".xcworkspace")
	}).First()
	if row == nil {
		row = outline.Descendants().ByRole("AXRow").First()
	}
	if row != nil {
		_ = row.Click()
		row.Release()
	}
}

func dismissActivateScheme(app *axuiautomation.Application) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, sheet := range app.Sheets().AllElements() {
			if btn := sheet.Buttons().ByTitle("Activate").First(); btn != nil {
				_ = btn.Click()
				return
			}
			if btn := sheet.Buttons().ByTitle("Don't Activate").First(); btn != nil {
				_ = btn.Click()
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}
