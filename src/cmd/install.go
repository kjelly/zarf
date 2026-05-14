// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2021-Present The Zarf Authors

// Package cmd contains the CLI commands for Zarf.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/zarf-dev/zarf/src/config/lang"
	"github.com/zarf-dev/zarf/src/pkg/logger"
)

func newInstallCommand() *cobra.Command {
	o := &packageDeployOptions{
		confirm: true,
	}

	cmd := &cobra.Command{
		Use:     "install PACKAGE_SOURCE [PACKAGE_SOURCE ...]",
		Aliases: []string{"i"},
		Short:   lang.CmdInstallShort,
		Long:    lang.CmdInstallLong,
		Args:    cobra.MinimumNArgs(1),
		PreRun:  o.preRun,
		RunE:    o.runInstall,
	}

	v := getViper()

	cmd.Flags().IntVar(&o.ociConcurrency, "oci-concurrency", v.GetInt(VPkgOCIConcurrency), lang.CmdPackageFlagConcurrency)
	cmd.Flags().StringVarP(&o.publicKeyPath, "key", "k", v.GetString(VPkgPublicKey), lang.CmdPackageFlagFlagPublicKey)

	cmd.Flags().BoolVar(&o.adoptExistingResources, "adopt-existing-resources", false, lang.CmdPackageDeployFlagAdoptExistingResources)
	cmd.Flags().BoolVar(&o.connected, "connected", v.GetBool(VPkgDeployConnected), lang.CmdPackageDeployFlagConnected)
	cmd.Flags().BoolVar(&o.forceConflicts, "force-conflicts", false, lang.CmdPackageDeployFlagForceConflicts)
	cmd.Flags().DurationVar(&o.timeout, "timeout", v.GetDuration(VPkgDeployTimeout), lang.CmdPackageDeployFlagTimeout)

	cmd.Flags().StringSliceVarP(&o.valuesFiles, "values", "v", GetStringSlice(v, VPkgDeployValues), lang.CmdPackageDeployFlagValuesFiles)
	cmd.Flags().IntVar(&o.retries, "retries", v.GetInt(VPkgRetries), lang.CmdPackageFlagRetries)
	cmd.Flags().StringToStringVar(&o.setVariables, "set", v.GetStringMapString(VPkgDeploySet), "Alias for --set-variables")
	_ = cmd.Flags().MarkDeprecated("set", "Use --set-variables instead")
	cmd.Flags().StringToStringVar(&o.setVariables, "set-variables", v.GetStringMapString(VPkgDeploySet), lang.CmdPackageDeployFlagSetVariables)
	cmd.Flags().StringToStringVar(&o.setValues, "set-values", v.GetStringMapString(VPkgDeploySetValues), lang.CmdPackageDeployFlagSetValues)
	cmd.Flags().StringVar(&o.optionalComponents, "components", v.GetString(VPkgDeployComponents), lang.CmdPackageDeployFlagComponents)
	cmd.Flags().StringVar(&o.shasum, "shasum", v.GetString(VPkgDeployShasum), lang.CmdPackageDeployFlagShasum)
	cmd.Flags().StringVarP(&o.namespaceOverride, "namespace", "n", v.GetString(VPkgDeployNamespace), lang.CmdPackageDeployFlagNamespace)
	cmd.Flags().BoolVar(&o.skipSignatureValidation, "skip-signature-validation", false, lang.CmdPackageFlagSkipSignatureValidation)
	cmd.Flags().BoolVar(&o.verify, "verify", v.GetBool(VPkgVerify), lang.CmdPackageFlagVerify)
	cmd.Flags().BoolVar(&o.skipVersionCheck, "skip-version-check", false, "Ignore version requirements when deploying the package")
	_ = cmd.Flags().MarkHidden("skip-version-check")
	errSig := cmd.Flags().MarkDeprecated("skip-signature-validation", "Signature verification now occurs on every execution, but is not enforced by default. Use --verify to enforce validation. This flag will be removed in Zarf v1.0.0.")
	if errSig != nil {
		logger.Default().Debug("unable to mark flag skip-signature-validation", "error", errSig)
	}

	return cmd
}

func (o *packageDeployOptions) runInstall(cmd *cobra.Command, args []string) (err error) {
	ctx := cmd.Context()

	ctx, values, cachePath, err := o.mergeAndPrepare(ctx)
	if err != nil {
		return err
	}

	for i, src := range args {
		if i > 0 {
			logger.From(ctx).Info("deploying next package", "source", src)
		}
		err = o.deployPackage(ctx, src, values, cachePath)
		if err != nil {
			return fmt.Errorf("failed to install package %q: %w", src, err)
		}
	}

	return nil
}
