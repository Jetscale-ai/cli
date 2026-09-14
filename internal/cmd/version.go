package cmd

import (
	"fmt"

	"github.com/Jetscale-ai/cli/internal/buildinfo"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
)

type versionInfo struct {
	Binary       string `json:"binary" yaml:"binary"`
	Version      string `json:"version" yaml:"version"`
	Commit       string `json:"commit" yaml:"commit"`
	Date         string `json:"date" yaml:"date"`
	Channel      string `json:"channel" yaml:"channel"`
	BackendSHA   string `json:"backend_sha" yaml:"backend_sha"`
	SchemaSHA256 string `json:"schema_sha256" yaml:"schema_sha256"`
	APIVersion   string `json:"api_version" yaml:"api_version"`
	SourceSHA256 string `json:"source_sha256" yaml:"source_sha256"`
	SourceDirty  string `json:"source_dirty" yaml:"source_dirty"`
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := printer(cmd)
			if err != nil {
				return err
			}
			info := versionInfo{
				Binary: cmd.Root().Name(), Version: version, Commit: commit, Date: date,
				Channel:    buildinfo.Channel,
				BackendSHA: provenance(buildinfo.BackendSHA), SchemaSHA256: provenance(buildinfo.SchemaSHA256),
				APIVersion: provenance(buildinfo.APIVersion), SourceSHA256: provenance(buildinfo.SourceSHA256),
				SourceDirty: provenance(buildinfo.SourceDirty),
			}
			if p.Format != output.Table {
				return p.Print(info, nil)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s (commit: %s, built: %s)\nchannel: %s\nbackend SHA: %s\nschema SHA256: %s\nAPI version: %s\nsource SHA256: %s\nsource dirty: %s\n",
				info.Binary, info.Version, info.Commit, info.Date, info.Channel,
				info.BackendSHA, info.SchemaSHA256, info.APIVersion, info.SourceSHA256, info.SourceDirty)
			return err
		},
	}
}

func provenance(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
