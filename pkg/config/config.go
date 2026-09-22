package config

import (
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/lintnet/lintnet/pkg/errlevel"
)

// getIgnoredPatterns returns ignored patterns.
// If ignoredDirs is nil, it returns default ignored patterns.
// An ignored pattern is "**/<ignored dir>/**", which is a pattern of doublestar.
func getIgnoredPatterns(ignoredDirs []string) []string {
	if ignoredDirs == nil {
		ignoredDirs = []string{
			".git",
			"node_modules",
		}
	}
	ignoredPatterns := make([]string, len(ignoredDirs))
	for i, d := range ignoredDirs {
		ignoredPatterns[i] = fmt.Sprintf("**/%s/**", d)
	}
	return ignoredPatterns
}

type Config struct {
	ErrorLevel      errlevel.Level            `json:"error_level,omitempty"`
	ShownErrorLevel errlevel.Level            `json:"shown_error_level,omitempty"`
	Targets         []*Target                 `json:"targets,omitempty"`
	Outputs         Outputs                   `json:"outputs,omitempty"`
	ModuleArchives  map[string]*ModuleArchive `json:"module_archives,omitempty"`
	IgnoredPatterns []string                  `json:"ignore_patterns,omitempty"`
	// MaxDataBytes is the maximum number of bytes lintnet reads from each data file.
	// Zero means no limit.
	MaxDataBytes int64 `json:"max_data_bytes,omitempty"`
}

func (c *Config) setErrorLevel(errLevel string) error {
	if errLevel == "" {
		c.ErrorLevel = errlevel.Error
		return nil
	}
	level, err := errlevel.New(errLevel)
	if err != nil {
		return fmt.Errorf("parse the error level: %w", err)
	}
	c.ErrorLevel = level
	return nil
}

func (c *Config) setShownErrorLevel(errLevel string) error {
	if errLevel == "" {
		c.ShownErrorLevel = errlevel.Info
		return nil
	}
	level, err := errlevel.New(errLevel)
	if err != nil {
		return fmt.Errorf("parse the error level: %w", err)
	}
	c.ShownErrorLevel = level
	return nil
}

// setIgnoredPatterns sets ignored patterns.
func (c *Config) setIgnoredPatterns(dirs []string) {
	c.IgnoredPatterns = getIgnoredPatterns(dirs)
	if c.IgnoredPatterns == nil {
		c.IgnoredPatterns = []string{
			"node_modules",
			".git",
		}
	}
}

type RawConfig struct {
	FilePath        string       `json:"-"`
	ErrorLevel      string       `json:"error_level,omitempty"`
	ShownErrorLevel string       `json:"shown_error_level,omitempty"`
	IgnoredDirs     []string     `json:"ignored_dirs,omitempty"`
	Targets         []*RawTarget `json:"targets"`
	Outputs         Outputs      `json:"outputs,omitempty"`
	// MaxDataBytes is a pointer so an explicitly configured value
	// can be distinguished from an unset one.
	MaxDataBytes *int64 `json:"max_data_bytes,omitempty"`
}

func (rc *RawConfig) GetTarget(targetID string) (*RawTarget, error) {
	for _, target := range rc.Targets {
		if target.ID == targetID {
			return target, nil
		}
	}
	return nil, errors.New("target isn't found")
}

// Parse processes a raw configuration.
func (rc *RawConfig) Parse() (*Config, error) {
	cfg := &Config{
		Targets: make([]*Target, len(rc.Targets)),
		Outputs: rc.Outputs,
	}
	cfg.setIgnoredPatterns(rc.IgnoredDirs)

	if rc.MaxDataBytes != nil {
		if *rc.MaxDataBytes <= 0 {
			return nil, errors.New("max_data_bytes must be a positive integer")
		}
		cfg.MaxDataBytes = *rc.MaxDataBytes
	}

	if err := cfg.setErrorLevel(rc.ErrorLevel); err != nil {
		return nil, err
	}

	if err := cfg.setShownErrorLevel(rc.ShownErrorLevel); err != nil {
		return nil, err
	}

	if cfg.ShownErrorLevel > cfg.ErrorLevel {
		// ShownErrorLevel should be lower than or equal to ErrorLevel.
		// If ShownErrorLevel is higher than ErrorLevel, it sets ShownErrorLevel to ErrorLevel.
		cfg.ShownErrorLevel = cfg.ErrorLevel
	}

	// moduleArchives is a map of modules.
	// Extract modules from configuration to install them.
	moduleArchives := map[string]*ModuleArchive{}
	for i, rt := range rc.Targets {
		target, err := rt.Parse()
		if err != nil {
			return nil, err
		}
		cfg.Targets[i] = target
		maps.Copy(moduleArchives, target.ModuleArchives)
	}
	if err := rc.Outputs.Preprocess(moduleArchives); err != nil {
		return nil, err
	}
	cfg.ModuleArchives = moduleArchives
	return cfg, nil
}

// ParseMaxDataBytes parses the value of the --max-data-bytes command line option.
// The value must be a positive integer.
func ParseMaxDataBytes(s string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("max_data_bytes must be a positive integer: %w", err)
	}
	if n <= 0 {
		return 0, errors.New("max_data_bytes must be a positive integer")
	}
	return n, nil
}
