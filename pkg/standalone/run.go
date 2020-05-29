// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package standalone

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v2"

	"github.com/Pallinder/sillyname-go"
	"github.com/phayes/freeport"

	"github.com/dapr/cli/utils"
	"github.com/dapr/dapr/pkg/components"
	modes "github.com/dapr/dapr/pkg/config/modes"
)

const (
	messageBusYamlFileName = "pubsub.yaml"
	stateStoreYamlFileName = "statestore.yaml"
	sentryDefaultAddress   = "localhost:50001"
)

// RunConfig represents the application configuration parameters.
type RunConfig struct {
	AppID           string
	AppPort         int
	HTTPPort        int
	GRPCPort        int
	ConfigFile      string
	Protocol        string
	Arguments       []string
	EnableProfiling bool
	ProfilePort     int
	LogLevel        string
	MaxConcurrency  int
	RedisHost       string
	PlacementHost   string
	ComponentsPath  string
}

// RunOutput represents the run output.
type RunOutput struct {
	DaprCMD      *exec.Cmd
	DaprHTTPPort int
	DaprGRPCPort int
	AppID        string
	AppCMD       *exec.Cmd
}

type component struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Type     string                  `yaml:"type"`
		Metadata []componentMetadataItem `yaml:"metadata"`
	} `yaml:"spec"`
}

type componentMetadataItem struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

func getDaprCommand(appID string, daprHTTPPort int, daprGRPCPort int, appPort int, configFile, protocol string, enableProfiling bool, profilePort int, logLevel string, maxConcurrency int, placementHost string) (*exec.Cmd, int, int, int, error) {
	if daprHTTPPort < 0 {
		port, err := freeport.GetFreePort()
		if err != nil {
			return nil, -1, -1, -1, err
		}

		daprHTTPPort = port
	}

	if daprGRPCPort < 0 {
		grpcPort, err := freeport.GetFreePort()
		if err != nil {
			return nil, -1, -1, -1, err
		}

		daprGRPCPort = grpcPort
	}

	if maxConcurrency < 1 {
		maxConcurrency = -1
	}

	daprCMD := "daprd"
	if runtime.GOOS == daprWindowsOS {
		daprCMD = fmt.Sprintf("%s.exe", daprCMD)
	}

	metricsPort, err := freeport.GetFreePort()
	if err != nil {
		return nil, -1, -1, -1, err
	}

	args := []string{"--app-id", appID, "--dapr-http-port", fmt.Sprintf("%v", daprHTTPPort), "--dapr-grpc-port", fmt.Sprintf("%v", daprGRPCPort), "--log-level", logLevel, "--max-concurrency", fmt.Sprintf("%v", maxConcurrency), "--protocol", protocol, "--metrics-port", fmt.Sprintf("%v", metricsPort)}
	if appPort > -1 {
		args = append(args, "--app-port", fmt.Sprintf("%v", appPort))
	}

	args = append(args, "--placement-address")

	if runtime.GOOS == daprWindowsOS {
		args = append(args, fmt.Sprintf("%s:6050", placementHost))
	} else {
		args = append(args, fmt.Sprintf("%s:50005", placementHost))
	}

	if configFile != "" {
		args = append(args, "--config", configFile)
		sentryAddress := mtlsEndpoint(configFile)
		if sentryAddress != "" {
			// mTLS is enabled locally, set it up
			args = append(args, "--enable-mtls", "--sentry-address", sentryAddress)
		}
	}

	if enableProfiling {
		if profilePort == -1 {
			pp, err := freeport.GetFreePort()
			if err != nil {
				return nil, -1, -1, -1, err
			}
			profilePort = pp
		}

		args = append(
			args,
			"--enable-profiling", "true",
			"--profile-port", fmt.Sprintf("%v", profilePort))
	}

	cmd := exec.Command(daprCMD, args...)
	return cmd, daprHTTPPort, daprGRPCPort, metricsPort, nil
}

func mtlsEndpoint(configFile string) string {
	if configFile == "" {
		return ""
	}

	b, err := ioutil.ReadFile(configFile)
	if err != nil {
		return ""
	}

	var config mtlsConfig
	err = yaml.Unmarshal(b, &config)
	if err != nil {
		return ""
	}

	if config.Spec.MTLS.Enabled {
		return sentryDefaultAddress
	}
	return ""
}

func getAppCommand(httpPort, grpcPort, metricsPort int, command string, args []string) (*exec.Cmd, error) {
	cmd := exec.Command(command, args...)
	cmd.Env = os.Environ()
	cmd.Env = append(
		cmd.Env,
		fmt.Sprintf("DAPR_HTTP_PORT=%v", httpPort),
		fmt.Sprintf("DAPR_GRPC_PORT=%v", grpcPort),
		fmt.Sprintf("DAPR_METRICS_PORT=%v", metricsPort))

	return cmd, nil
}

func createRedisStateStore(redisHost string, componentsPath string) error {
	redisStore := component{
		APIVersion: "dapr.io/v1alpha1",
		Kind:       "Component",
	}

	redisStore.Metadata.Name = "statestore"
	redisStore.Spec.Type = "state.redis"
	redisStore.Spec.Metadata = []componentMetadataItem{
		{
			Name:  "redisHost",
			Value: fmt.Sprintf("%s:6379", redisHost),
		},
		{
			Name:  "redisPassword",
			Value: "",
		},
		{
			Name:  "actorStateStore",
			Value: "true",
		},
	}

	b, err := yaml.Marshal(&redisStore)
	if err != nil {
		return err
	}

	filePath := filepath.Join(componentsPath, stateStoreYamlFileName)
	fmt.Printf("WARNING: Redis State Store file is being overwritten: %s\n", filePath)
	err = ioutil.WriteFile(filePath, b, 0644)
	if err != nil {
		return err
	}

	return nil
}

func createRedisPubSub(redisHost string, componentsPath string) error {
	redisPubSub := component{
		APIVersion: "dapr.io/v1alpha1",
		Kind:       "Component",
	}

	redisPubSub.Metadata.Name = "pubsub"
	redisPubSub.Spec.Type = "pubsub.redis"
	redisPubSub.Spec.Metadata = []componentMetadataItem{
		{
			Name:  "redisHost",
			Value: fmt.Sprintf("%s:6379", redisHost),
		},
		{
			Name:  "redisPassword",
			Value: "",
		},
	}

	b, err := yaml.Marshal(&redisPubSub)
	if err != nil {
		return err
	}

	filePath := filepath.Join(componentsPath, messageBusYamlFileName)
	fmt.Printf("WARNING: Redis PubSub file is being overwritten: %s\n", filePath)
	err = ioutil.WriteFile(filePath, b, 0644)
	if err != nil {
		return err
	}

	return nil
}

func Run(config *RunConfig) (*RunOutput, error) {
	appID := config.AppID
	if appID == "" {
		appID = strings.Replace(sillyname.GenerateStupidName(), " ", "-", -1)
	}

	dapr, err := List()
	if err != nil {
		return nil, err
	}

	for _, a := range dapr {
		if appID == a.AppID {
			return nil, fmt.Errorf("dapr with ID %s is already running", appID)
		}
	}

	componentsPath, err := getComponentsPath(config)
	if err != nil {
		return nil, err
	}

	componentsLoader := components.NewStandaloneComponents(modes.StandaloneConfig{ComponentsPath: componentsPath})
	components, err := componentsLoader.LoadComponents()
	if err != nil {
		return nil, err
	}

	var stateStore, pubSub string

	for _, component := range components {
		if strings.HasPrefix(component.Spec.Type, "state") {
			stateStore = component.Spec.Type
		}
		if strings.HasPrefix(component.Spec.Type, "pubsub") {
			pubSub = component.Spec.Type
		}
	}

	if stateStore == "" {
		err = createRedisStateStore(config.RedisHost, componentsPath)
		if err != nil {
			return nil, err
		}
	}

	if pubSub == "" {
		err = createRedisPubSub(config.RedisHost, componentsPath)
		if err != nil {
			return nil, err
		}
	}

	daprCMD, daprHTTPPort, daprGRPCPort, metricsPort, err := getDaprCommand(appID, config.HTTPPort, config.GRPCPort, config.AppPort, config.ConfigFile, config.Protocol, config.EnableProfiling, config.ProfilePort, config.LogLevel, config.MaxConcurrency, config.PlacementHost)
	if err != nil {
		return nil, err
	}

	for _, a := range dapr {
		if daprHTTPPort == a.HTTPPort {
			return nil, fmt.Errorf("there's already a Dapr instance running with http port %v", daprHTTPPort)
		} else if daprGRPCPort == a.GRPCPort {
			return nil, fmt.Errorf("there's already a Dapr instance running with gRPC port %v", daprGRPCPort)
		}
	}

	argCount := len(config.Arguments)
	runArgs := []string{}
	var appCMD *exec.Cmd

	if argCount > 0 {
		cmd := config.Arguments[0]
		if len(config.Arguments) > 1 {
			runArgs = config.Arguments[1:]
		}

		appCMD, err = getAppCommand(daprHTTPPort, daprGRPCPort, metricsPort, cmd, runArgs)
		if err != nil {
			return nil, err
		}
	}

	return &RunOutput{
		DaprCMD:      daprCMD,
		AppCMD:       appCMD,
		AppID:        appID,
		DaprHTTPPort: daprHTTPPort,
		DaprGRPCPort: daprGRPCPort,
	}, nil
}

func getComponentsPath(config *RunConfig) (string, error) {
	if config.ComponentsPath == "" {
		componentsPath := utils.GetDefaultComponentsFolder()
		fmt.Println("Read components: ", componentsPath)
		return componentsPath, nil
	}

	_, err := os.Stat(config.ComponentsPath)
	return config.ComponentsPath, err
}
