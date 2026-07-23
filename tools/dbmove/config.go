package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// VolumeSpec names a source PVC and the deployment that mounts it.
type VolumeSpec struct {
	PVCName   string `yaml:"pvcName"`
	MountedBy string `yaml:"mountedBy"`
}

// MoveConfig is the full description of one app move.
type MoveConfig struct {
	App              string       `yaml:"app"`
	BegetContext     string       `yaml:"begetContext"`
	MgmtContext      string       `yaml:"mgmtContext"`
	SrcProject       string       `yaml:"srcProject"`
	SrcEnv           string       `yaml:"srcEnv"`
	SrcNamespace     string       `yaml:"srcNamespace"`
	TargetProject    string       `yaml:"targetProject"`
	TargetEnv        string       `yaml:"targetEnv"`
	TargetNamespace  string       `yaml:"targetNamespace"`
	DBDatname        string       `yaml:"dbDatname"`
	DBCredSecret     string       `yaml:"dbCredSecret"`
	ArgoInfraPath    string       `yaml:"argoInfraPath"`
	AppFolderRel     string       `yaml:"appFolderRel"`
	Volumes          []VolumeSpec `yaml:"volumes"`
	OOBSecrets       []string     `yaml:"oobSecrets"`
	ScaleDeployments []string     `yaml:"scaleDeployments"`
}

// LoadConfig reads and validates a MoveConfig from a YAML file.
func LoadConfig(path string) (MoveConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return MoveConfig{}, err
	}
	return loadConfigBytes(b)
}

// loadConfigBytes parses+validates config YAML bytes.
func loadConfigBytes(b []byte) (MoveConfig, error) {
	var c MoveConfig
	if err := yaml.Unmarshal(b, &c); err != nil {
		return MoveConfig{}, err
	}
	if c.App == "" || c.SrcNamespace == "" || c.TargetProject == "" || c.TargetEnv == "" {
		return MoveConfig{}, fmt.Errorf("config: app, srcNamespace, targetProject, targetEnv are required")
	}
	if c.TargetNamespace == "" {
		c.TargetNamespace = c.TargetProject + "-" + c.TargetEnv
	}
	return c, nil
}
