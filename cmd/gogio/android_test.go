// SPDX-License-Identifier: Unlicense OR MIT

package main_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type AndroidTestDriver struct {
	driverBase

	sdkDir  string
	adbPath string
}

var rxAdbDevice = regexp.MustCompile(`(.*)\s+device$`)

func (d *AndroidTestDriver) Start(path string, width, height int) {
	d.sdkDir = os.Getenv("ANDROID_HOME")
	if d.sdkDir == "" {
		d.Skipf("Android SDK is required; set $ANDROID_HOME")
	}
	d.adbPath = filepath.Join(d.sdkDir, "platform-tools", "adb")

	devOut := bytes.TrimSpace(d.adb("devices"))
	devices := rxAdbDevice.FindAllSubmatch(devOut, -1)
	switch len(devices) {
	case 0:
		d.Skipf("no Android devices attached via adb; skipping")
	case 1:
	default:
		d.Skipf("multiple Android devices attached via adb; skipping")
	}

	// If the device is attached but asleep, it's probably just charging.
	// Don't use it; the screen needs to be on and unlocked for the test to
	// work.
	if !bytes.Contains(
		d.adb("shell", "dumpsys", "power"),
		[]byte(" mWakefulness=Awake"),
	) {
		d.Skipf("Android device isn't awake; skipping")
	}

	// First, build the app.
	dir, err := ioutil.TempDir("", "gio-endtoend-android")
	if err != nil {
		d.Fatal(err)
	}
	d.Cleanup(func() { os.RemoveAll(dir) })
	apk := filepath.Join(dir, "e2e.apk")

	// TODO(mvdan): This is inefficient, as we link the gogio tool every time.
	// Consider options in the future. On the plus side, this is simple.
	cmd := exec.Command("go", "run", ".", "-target=android", "-appid="+appid, "-o="+apk, path)
	if out, err := cmd.CombinedOutput(); err != nil {
		d.Fatalf("could not build app: %s:\n%s", err, out)
	}

	// Make sure the app isn't installed already, and try to uninstall it
	// when we finish. Previous failed test runs might have left the app.
	d.tryUninstall()
	d.adb("install", apk)
	d.Cleanup(d.tryUninstall)

	// Force our e2e app to be fullscreen, so that the android system bar at
	// the top doesn't mess with our screenshots.
	// TODO(mvdan): is there a way to do this via gio, so that we don't need
	// to set up a global Android setting via the shell?
	d.adb("shell", "settings", "put", "global", "policy_control", "immersive.full="+appid)

	// Make sure the app isn't already running.
	d.adb("shell", "pm", "clear", appid)

	// Start listening for log messages.
	{
		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, d.adbPath,
			"logcat",
			"-s",    // suppress other logs
			"-T1",   // don't show prevoius log messages
			"gio:*", // show all logs from gio
		)
		logcat, err := cmd.StdoutPipe()
		if err != nil {
			d.Fatal(err)
		}
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			d.Fatal(err)
		}
		d.Cleanup(cancel)
		go func() {
			scanner := bufio.NewScanner(logcat)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasSuffix(line, ": frame ready") {
					d.frameNotifs <- true
				}
			}
		}()
	}

	// Start the app.
	d.adb("shell", "monkey", "-p", appid, "1")

	// Unfortunately, it seems like waiting for the initial frame isn't
	// enough. Most Android versions have animations when opening apps that
	// run for hundreds of milliseconds, so that's probably the reason.
	// TODO(mvdan): any way to wait for the screen to be ready without a
	// static sleep?
	time.Sleep(500 * time.Millisecond)

	// Wait for the gio app to render.
	d.waitForFrame()
}

func (d *AndroidTestDriver) Screenshot() image.Image {
	out := d.adb("shell", "screencap", "-p")
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		d.Fatal(err)
	}
	return img
}

func (d *AndroidTestDriver) tryUninstall() {
	cmd := exec.Command(d.adbPath, "shell", "pm", "uninstall", appid)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if bytes.Contains(out, []byte("Unknown package")) {
			// The package is not installed. Don't log anything.
			return
		}
		d.Logf("could not uninstall: %v\n%s", err, out)
	}
}

func (d *AndroidTestDriver) adb(args ...interface{}) []byte {
	strs := []string{}
	for _, arg := range args {
		strs = append(strs, fmt.Sprint(arg))
	}
	cmd := exec.Command(d.adbPath, strs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		d.Errorf("%s", out)
		d.Fatal(err)
	}
	return out
}

func (d *AndroidTestDriver) Click(x, y int) {
	d.adb("shell", "input", "tap", x, y)

	// Wait for the gio app to render after this click.
	d.waitForFrame()
}
