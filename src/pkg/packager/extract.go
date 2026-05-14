// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2021-Present The Zarf Authors

package packager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/defenseunicorns/pkg/helpers/v2"
	"github.com/zarf-dev/zarf/src/config"
	"github.com/zarf-dev/zarf/src/internal/packager/helm"
	"github.com/zarf-dev/zarf/src/internal/packager/template"
	"github.com/zarf-dev/zarf/src/pkg/packager/layout"
	"github.com/zarf-dev/zarf/src/pkg/state"
	"github.com/zarf-dev/zarf/src/pkg/utils"
	"github.com/zarf-dev/zarf/src/pkg/value"
	"github.com/zarf-dev/zarf/src/types"
)

// HelmInstallCommand describes a single helm install command that can be executed
type HelmInstallCommand struct {
	ComponentName  string
	ChartName      string
	ReleaseName    string
	Namespace      string
	ChartFileName  string
	ValuesFileName string
	Version        string
}

// ExtractResult holds the results of extracting charts from a Zarf package
type ExtractResult struct {
	PackageName string
	OutputDir   string
	Commands    []HelmInstallCommand
	HasImages   bool
}

// ExtractOptions are the optional parameters to ExtractPackage
type ExtractOptions struct {
	SetVariables map[string]string
	Values       value.Values
	OutputDir    string
	SkipImages   bool
	types.RemoteOptions
}

// ExtractPackage extracts Helm charts and merged values from a loaded Zarf package to a directory.
func ExtractPackage(ctx context.Context, pkgLayout *layout.PackageLayout, opts ExtractOptions) (*ExtractResult, error) {
	s, err := state.Default()
	if err != nil {
		return nil, fmt.Errorf("unable to get default state: %w", err)
	}

	variableConfig, err := getPopulatedVariableConfig(ctx, pkgLayout.Pkg, opts.SetVariables, false)
	if err != nil {
		return nil, err
	}

	valuesPath := filepath.Join(pkgLayout.DirPath(), layout.ValuesYAML)
	vals, err := value.ParseLocalFile(ctx, valuesPath)
	if err != nil {
		return nil, err
	}
	vals.DeepMerge(opts.Values)

	if pkgLayout.Pkg.Values.Schema != "" {
		schemaPath := filepath.Join(pkgLayout.DirPath(), layout.ValuesSchema)
		if err := vals.Validate(ctx, schemaPath, value.ValidateOptions{SkipRequired: true}); err != nil {
			return nil, fmt.Errorf("extract values validation failed: %w", err)
		}
	}

	tmpPackagePath, err := utils.MakeTempDir(config.CommonOptions.TempDirectory)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = os.RemoveAll(tmpPackagePath)
	}()

	pkgName := pkgLayout.Pkg.Metadata.Name
	pkgOutputDir := filepath.Join(opts.OutputDir, pkgName)
	if err := os.MkdirAll(pkgOutputDir, helpers.ReadWriteExecuteUser); err != nil {
		return nil, fmt.Errorf("unable to create output directory %s: %w", pkgOutputDir, err)
	}

	var commands []HelmInstallCommand
	hasImages := false

	for _, component := range pkgLayout.Pkg.Components {
		if len(component.Charts) > 0 {
			appTmpls, err := template.GetZarfTemplates(ctx, component.Name, s)
			if err != nil {
				return nil, err
			}
			variableConfig.SetApplicationTemplates(appTmpls)

			tmpComponentPath := filepath.Join(tmpPackagePath, component.Name)
			if err := os.MkdirAll(tmpComponentPath, helpers.ReadWriteExecuteUser); err != nil {
				return nil, err
			}

			chartDir, err := pkgLayout.GetComponentDir(ctx, tmpComponentPath, component.Name, layout.ChartsComponentDir)
			if err != nil {
				return nil, fmt.Errorf("failed to get charts for component %s: %w", component.Name, err)
			}

			valuesDir, err := pkgLayout.GetComponentDir(ctx, tmpComponentPath, component.Name, layout.ValuesComponentDir)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("failed to get values for component %s: %w", component.Name, err)
			}

			for _, chart := range component.Charts {
				if err := templateValuesFiles(chart, valuesDir, variableConfig); err != nil {
					return nil, err
				}

				chartOverrides, err := generateValuesOverrides(ctx, chart, component.Name, overrideOpts{
					variableConfig: variableConfig,
					values:         vals,
				})
				if err != nil {
					return nil, err
				}

				helmChart, mergedValues, err := helm.LoadChartData(chart, chartDir, valuesDir, chartOverrides)
				if err != nil {
					return nil, fmt.Errorf("failed to load chart data for chart %s: %w", chart.Name, err)
				}

				compOutDir := filepath.Join(pkgOutputDir, component.Name)

				chartsOutDir := filepath.Join(compOutDir, "charts")
				if err := os.MkdirAll(chartsOutDir, helpers.ReadWriteExecuteUser); err != nil {
					return nil, fmt.Errorf("unable to create charts directory: %w", err)
				}

				valuesOutDir := filepath.Join(compOutDir, "values")
				if err := os.MkdirAll(valuesOutDir, helpers.ReadWriteExecuteUser); err != nil {
					return nil, fmt.Errorf("unable to create values directory: %w", err)
				}

				chartTgzName := helm.StandardName("", chart) + ".tgz"
				chartTgzPath := filepath.Join(chartDir, chartTgzName)
				dstChartPath := filepath.Join(chartsOutDir, chartTgzName)
				if err := copyFile(chartTgzPath, dstChartPath); err != nil {
					return nil, fmt.Errorf("failed to copy chart %s: %w", chart.Name, err)
				}

				valuesYaml, err := mergedValues.YAML()
				if err != nil {
					return nil, fmt.Errorf("failed to marshal values for chart %s: %w", chart.Name, err)
				}
				valuesFileName := helm.StandardName("", chart) + "-values.yaml"
				valuesOutPath := filepath.Join(valuesOutDir, valuesFileName)
				if err := os.WriteFile(valuesOutPath, []byte(valuesYaml), helpers.ReadWriteUser); err != nil {
					return nil, fmt.Errorf("failed to write values file for chart %s: %w", chart.Name, err)
				}

				releaseName := chart.ReleaseName
				if releaseName == "" {
					releaseName = helmChart.Metadata.Name
				}
				namespace := chart.Namespace
				if namespace == "" {
					namespace = "default"
				}

				commands = append(commands, HelmInstallCommand{
					ComponentName:  component.Name,
					ChartName:      helmChart.Metadata.Name,
					ReleaseName:    releaseName,
					Namespace:      namespace,
					ChartFileName:  chartTgzName,
					ValuesFileName: valuesFileName,
					Version:        chart.Version,
				})
			}
		}

		if !hasImages && len(component.GetImages()) > 0 {
			hasImages = true
		}
	}

	if hasImages && !opts.SkipImages {
		imagesDir := pkgLayout.GetImageDirPath()
		imagesOutDir := filepath.Join(pkgOutputDir, "images")
		if err := copyDirectory(imagesDir, imagesOutDir); err != nil {
			return nil, fmt.Errorf("failed to extract images: %w", err)
		}
	}

	if err := writeInstallScript(pkgOutputDir, commands, hasImages && !opts.SkipImages); err != nil {
		return nil, err
	}

	return &ExtractResult{
		PackageName: pkgName,
		OutputDir:   pkgOutputDir,
		Commands:    commands,
		HasImages:   hasImages && !opts.SkipImages,
	}, nil
}

func writeInstallScript(outputDir string, commands []HelmInstallCommand, hasImages bool) error {
	var sb strings.Builder
	sb.WriteString("#!/usr/bin/env bash\n")
	sb.WriteString("# Generated by zarf extract\n")
	sb.WriteString("set -euo pipefail\n\n")
	sb.WriteString("SCRIPT_DIR=\"$(cd \"$(dirname \"${BASH_SOURCE[0]}\")\" && pwd)\"\n\n")

	if hasImages {
		sb.WriteString("# Container images are available in OCI layout format in the images/ directory.\n")
		sb.WriteString("# To push images to a registry using crane:\n")
		sb.WriteString("#   zarf tools registry push ./images <registry-url>/<image-name>:<tag>\n")
		sb.WriteString("#   skopeo copy oci:./images docker://<registry-url>/<image-name>:<tag>\n")
		sb.WriteString("#\n")
	}

	for _, cmd := range commands {
		sb.WriteString(fmt.Sprintf("# Component: %s, Chart: %s\n", cmd.ComponentName, cmd.ChartName))
		sb.WriteString(fmt.Sprintf("helm install %s \\\n", cmd.ReleaseName))
		sb.WriteString(fmt.Sprintf("  \"$SCRIPT_DIR/%s/charts/%s\" \\\n", cmd.ComponentName, cmd.ChartFileName))
		sb.WriteString(fmt.Sprintf("  --namespace %s \\\n", cmd.Namespace))
		sb.WriteString(fmt.Sprintf("  --values \"$SCRIPT_DIR/%s/values/%s\" \\\n", cmd.ComponentName, cmd.ValuesFileName))
		sb.WriteString("  --create-namespace\n\n")
	}

	scriptPath := filepath.Join(outputDir, "install.sh")
	if err := os.WriteFile(scriptPath, []byte(sb.String()), 0o755); err != nil {
		return fmt.Errorf("failed to write install script: %w", err)
	}
	return nil
}

func copyDirectory(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}
		return copyFile(path, dstPath)
	})
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, helpers.ReadWriteUser)
}
