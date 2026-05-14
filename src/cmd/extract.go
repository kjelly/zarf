// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2021-Present The Zarf Authors

// Package cmd contains the CLI commands for Zarf.
package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/zarf-dev/zarf/src/config"
	"github.com/zarf-dev/zarf/src/config/lang"
	"github.com/zarf-dev/zarf/src/pkg/logger"
	"github.com/zarf-dev/zarf/src/pkg/packager"
	"github.com/zarf-dev/zarf/src/pkg/packager/filters"
	"github.com/zarf-dev/zarf/src/pkg/packager/layout"
)

type extractOptions struct {
	outputDir    string
	setVariables map[string]string
	skipImages   bool
}

func newExtractCommand() *cobra.Command {
	o := &extractOptions{}

	cmd := &cobra.Command{
		Use:   "extract PACKAGE_SOURCE [PACKAGE_SOURCE ...]",
		Short: lang.CmdExtractShort,
		Long:  lang.CmdExtractLong,
		Args:  cobra.MinimumNArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return o.run(cmd, args) },
	}

	cmd.Flags().StringVarP(&o.outputDir, "output", "o", "zarf-extracted", "Directory to extract charts and values into")
	cmd.Flags().StringToStringVar(&o.setVariables, "set-variables", nil, lang.CmdPackageDeployFlagSetVariables)
	cmd.Flags().BoolVar(&o.skipImages, "no-images", false, "Skip extracting container images")

	return cmd
}

func (o *extractOptions) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	l := logger.From(ctx)

	for i, pkgSource := range args {
		if i > 0 {
			l.Info("extracting next package", "source", pkgSource)
		}

		if err := o.extractPackage(ctx, pkgSource); err != nil {
			return fmt.Errorf("failed to extract package %q: %w", pkgSource, err)
		}
	}

	return nil
}

func (o *extractOptions) extractPackage(ctx context.Context, pkgSource string) error {
	cachePath, err := getCachePath(ctx)
	if err != nil {
		return err
	}

	filter := filters.Combine(
		filters.ByLocalOS(config.GetArch()),
	)

	loadOpt := packager.LoadOptions{
		VerificationStrategy: layout.VerifyIfPossible,
		Filter:               filter,
		Architecture:         config.GetArch(),
		CachePath:            cachePath,
		RemoteOptions:        defaultRemoteOptions(),
	}

	pkgLayout, err := packager.LoadPackage(ctx, pkgSource, loadOpt)
	if err != nil {
		return fmt.Errorf("unable to load package: %w", err)
	}
	defer func() {
		_ = pkgLayout.Cleanup()
	}()

	absOutput, err := filepath.Abs(o.outputDir)
	if err != nil {
		return fmt.Errorf("invalid output path: %w", err)
	}

	result, err := packager.ExtractPackage(ctx, pkgLayout, packager.ExtractOptions{
		SetVariables: o.setVariables,
		OutputDir:    absOutput,
		SkipImages:   o.skipImages,
	})
	if err != nil {
		return err
	}

	l := logger.From(ctx)
	l.Info("extracted package", "name", result.PackageName, "path", result.OutputDir)

	if result.HasImages {
		l.Info("container images extracted", "path", filepath.Join(result.OutputDir, "images"))
	}

	for _, helmCmd := range result.Commands {
		l.Info("helm command available",
			"component", helmCmd.ComponentName,
			"chart", helmCmd.ChartName,
			"release", helmCmd.ReleaseName,
			"namespace", helmCmd.Namespace,
		)
	}

	installScript := filepath.Join(result.OutputDir, "install.sh")
	if _, err := os.Stat(installScript); err == nil {
		l.Info("install script generated", "path", installScript)
	}

	return nil
}
