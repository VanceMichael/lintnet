package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/suzuki-shunsuke/go-error-with-exit-code/ecerror"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
	"github.com/suzuki-shunsuke/urfave-cli-v3-util/urfave"
	"github.com/urfave/cli/v3"
)

const flagFile = "file"

type GlobalFlags struct {
	LogLevel string
	Config   string
}

func Run(ctx context.Context, logger *slogutil.Logger, env *urfave.Env) error {
	ctx, stop := notifySignal(ctx)
	defer stop()
	gFlags := &GlobalFlags{}
	err := urfave.Command(env, &cli.Command{ //nolint:wrapcheck
		Name:  "lintnet",
		Usage: "Powerful, Secure, Shareable Linter Powered by Jsonnet. https://lintnet.github.io/",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "log-level",
				Usage:       "log level",
				Sources:     cli.EnvVars("LINTNET_LOG_LEVEL"),
				Destination: &gFlags.LogLevel,
			},
			&cli.StringFlag{
				Name:        "config",
				Aliases:     []string{"c"},
				Usage:       "Configuration file path",
				Sources:     cli.EnvVars("LINTNET_CONFIG"),
				Destination: &gFlags.Config,
			},
		},
		Commands: []*cli.Command{
			(&lintCommand{
				version: env.Version,
			}).command(logger, gFlags),
			(&infoCommand{
				version: env.Version,
			}).command(logger, gFlags),
			(&initCommand{}).command(logger, gFlags),
			(&testCommand{
				version: env.Version,
			}).command(logger, gFlags),
			(&newCommand{}).command(logger, gFlags),
		},
	}).Run(ctx, env.Args)
	if errors.Is(err, context.Canceled) {
		// Cancellation is an observable lifecycle, not an ordinary command error.
		// Don't print the stack of the canceled work; exit with the conventional
		// signal cancellation code.
		fmt.Fprintln(os.Stderr, "lintnet: canceled")
		return ecerror.Wrap(urfave.ErrSilent, exitCodeCanceled)
	}
	return err
}
