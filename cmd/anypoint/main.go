// Command anypoint is a compact, dependency-free CLI for the MuleSoft Anypoint
// Platform. Its command tree follows anypoint-cli-v4, whose plugins it covers.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := runContext(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	if err != nil {
		if errors.Is(err, errHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func commands() *Cmd {
	return group("", "anypoint — compact CLI for the MuleSoft Anypoint Platform",
		leaf("login", "", "Authenticate (saving the credentials given as flags) and show identity", cmdLogin),
		leaf("logout", "", "Forget the cached access token", cmdLogout),
		leaf("whoami", "", "Show the authenticated user or connected app", cmdWhoami),
		group("config", "Manage profiles",
			leaf("list", "", "List profiles", cmdConfigList).alias("ls"),
			leaf("get", "[KEY]", "Show profile settings", cmdConfigGet),
			leaf("set", "KEY VALUE", "Set a profile value (VALUE - reads stdin)", cmdConfigSet),
			leaf("unset", "KEY", "Remove a profile value", cmdConfigUnset),
			leaf("use", "PROFILE", "Switch the current profile", cmdConfigUse),
		),
		accountCmd(),
		exchangeCmd(),
		secretsCmd(),
		apiMgrCmd(),
		runtimeMgrCmd(),
		cloudhubCmd(),
		designCenterCmd(),
		dataGraphCmd(),
		governanceCmd(),
		agentNetworkCmd(),
		dxCmd(),
		leaf("api", "PATH", "Call any Anypoint REST endpoint ({org}/{env} expand)", cmdAPI),
		leaf("version", "", "Print version", func(x *Ctx, _ []string) error {
			fmt.Fprintf(x.Out, "anypoint %s (library %s, %s/%s)\n", version, anypoint.Version, runtime.GOOS, runtime.GOARCH)
			return nil
		}),
	)
}
