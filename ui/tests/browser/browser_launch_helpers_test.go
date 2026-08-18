//go:build browser

package browser_test

import (
	"context"
	"os/exec"
	"runtime"
	"strings"

	"github.com/chromedp/chromedp"
)

const macAppCodeSignCloneFeature = "MacAppCodeSignClone"

func newBrowserExecAllocator(parent context.Context, options ...chromedp.ExecAllocatorOption) (context.Context, context.CancelFunc) {
	if runtime.GOOS == "darwin" {
		options = append(options, chromedp.ModifyCmdFunc(appendMacAppCodeSignCloneFeature))
	}
	return chromedp.NewExecAllocator(parent, options...)
}

func appendMacAppCodeSignCloneFeature(command *exec.Cmd) {
	const prefix = "--disable-features="
	for index, argument := range command.Args {
		if !strings.HasPrefix(argument, prefix) {
			continue
		}
		features := strings.Split(strings.TrimPrefix(argument, prefix), ",")
		for _, feature := range features {
			if feature == macAppCodeSignCloneFeature {
				return
			}
		}
		if len(features) == 1 && features[0] == "" {
			command.Args[index] = prefix + macAppCodeSignCloneFeature
		} else {
			command.Args[index] += "," + macAppCodeSignCloneFeature
		}
		return
	}
	command.Args = append(command.Args, prefix+macAppCodeSignCloneFeature)
}

func browserCandidates(goos string) []string {
	if goos == "darwin" {
		return []string{
			"/opt/homebrew/bin/chromium",
			"/usr/local/bin/chromium",
			"chromium",
			"chromium-browser",
			"google-chrome",
			"chrome",
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		}
	}
	return []string{"chromium", "chromium-browser", "google-chrome", "chrome"}
}
