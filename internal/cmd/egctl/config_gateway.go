// Copyright Envoy Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package egctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"sigs.k8s.io/yaml"

	kube "github.com/envoyproxy/gateway/internal/kubernetes"
	"github.com/envoyproxy/gateway/internal/utils"
)

const (
	envoyGatewayConfigDumpPath = "/api/config_dump"
)

type gatewayConfigDump map[string]map[string]interface{}

func gatewayCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "envoy-gateway",
		Aliases: []string{"gateway", "eg"},
		Short:   "Retrieves Envoy Gateway configuration dump.",
		Long:    "Retrieves configuration information from the Envoy Gateway admin config dump endpoint.",
		RunE: func(c *cobra.Command, args []string) error {
			return c.Help()
		},
	}

	cmd.AddCommand(gatewayAllConfigCommand())
	cmd.AddCommand(gatewaySummaryConfigCommand())

	return cmd
}

func gatewayAllConfigCommand() *cobra.Command {
	return gatewayConfigCommand("all", true, "Retrieves full configuration dump from Envoy Gateway.", `  # Retrieve full configuration dump from Envoy Gateway
  egctl config envoy-gateway all

  # Retrieve configuration dump from a specific pod
  egctl config envoy-gateway all <pod-name> -n <pod-namespace>

  # Retrieve configuration dump with short syntax
  egctl c eg all
`)
}

func gatewaySummaryConfigCommand() *cobra.Command {
	return gatewayConfigCommand("summary", false, "Retrieves summary configuration dump from Envoy Gateway.", `  # Retrieve summary configuration dump from Envoy Gateway
  egctl config envoy-gateway summary

  # Retrieve summary configuration dump from a specific pod
  egctl config envoy-gateway summary <pod-name> -n <pod-namespace>

  # Retrieve summary configuration dump with short syntax
  egctl c eg summary
`)
}

func gatewayConfigCommand(name string, includeAll bool, short string, example string) *cobra.Command {
	configCmd := &cobra.Command{
		Use:     fmt.Sprintf("%s [pod-name]", name),
		Short:   short,
		Example: example,
		Args:    cobra.MaximumNArgs(1),
		Run: func(c *cobra.Command, args []string) {
			cmdutil.CheckErr(runGatewayConfig(c, args, includeAll))
		},
	}

	return configCmd
}

func runGatewayConfig(c *cobra.Command, args []string, includeAll bool) error {
	configDump, err := retrieveGatewayConfigDump(args, includeAll)
	if err != nil {
		return err
	}

	out, err := marshalGatewayConfigDump(configDump, output)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(c.OutOrStdout(), string(out))
	return err
}

func retrieveGatewayConfigDump(args []string, includeAll bool) (gatewayConfigDump, error) {
	if !allNamespaces {
		if len(labelSelectors) == 0 {
			if len(args) != 0 && args[0] != "" {
				podName = args[0]
			}
		}

		if podNamespace == "" {
			return nil, fmt.Errorf("pod namespace is required")
		}
	}

	cli, err := getCLIClient()
	if err != nil {
		return nil, err
	}

	pods, err := fetchRunningEnvoyGatewayPods(cli, types.NamespacedName{Namespace: podNamespace, Name: podName}, labelSelectors, allNamespaces)
	if err != nil {
		return nil, err
	}

	podConfigDumps := make(gatewayConfigDump)
	mu := sync.Mutex{}
	for _, pod := range pods {
		if _, ok := podConfigDumps[pod.Namespace]; !ok {
			podConfigDumps[pod.Namespace] = make(map[string]interface{})
		}
	}

	var errs error
	var errMu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(len(pods))
	for _, pod := range pods {
		go func(pod types.NamespacedName) {
			defer wg.Done()

			fw, err := portForwarder(cli, pod, envoyGatewayAdminPort)
			if err != nil {
				errMu.Lock()
				errs = errors.Join(errs, err)
				errMu.Unlock()
				return
			}

			if err := fw.Start(); err != nil {
				errMu.Lock()
				errs = errors.Join(errs, err)
				errMu.Unlock()
				return
			}
			defer fw.Stop()

			configDump, err := extractGatewayConfigDump(fw, includeAll)
			if err != nil {
				errMu.Lock()
				errs = errors.Join(errs, err)
				errMu.Unlock()
				return
			}

			mu.Lock()
			podConfigDumps[pod.Namespace][pod.Name] = configDump
			mu.Unlock()
		}(pod)
	}

	wg.Wait()
	if errs != nil {
		return nil, errs
	}

	return podConfigDumps, nil
}

func fetchRunningEnvoyGatewayPods(c kube.CLIClient, nn types.NamespacedName, labelSelectors []string, allNamespaces bool) ([]types.NamespacedName, error) {
	var pods []corev1.Pod
	selectors := labelSelectors
	if len(selectors) == 0 {
		selectors = []string{"control-plane=envoy-gateway"}
	}

	switch {
	case allNamespaces:
		namespaces, err := c.Kube().CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for i := range namespaces.Items {
			podList, err := c.PodsForSelector(namespaces.Items[i].Name, selectors...)
			if err != nil {
				return nil, fmt.Errorf("list pods failed in ns %s: %w", namespaces.Items[i].Name, err)
			}

			if len(podList.Items) == 0 {
				continue
			}

			pods = append(pods, podList.Items...)
		}
	case len(labelSelectors) > 0:
		podList, err := c.PodsForSelector(nn.Namespace, labelSelectors...)
		if err != nil {
			return nil, fmt.Errorf("get pod %s fail: %w", nn, err)
		}

		if len(podList.Items) == 0 {
			return nil, fmt.Errorf("no Pods found for label selectors %+v", labelSelectors)
		}

		pods = podList.Items
	case nn.Name != "":
		pod, err := c.Pod(nn)
		if err != nil {
			return nil, fmt.Errorf("get pod %s fail: %w", nn, err)
		}

		pods = []corev1.Pod{*pod}
	default:
		podList, err := c.PodsForSelector(nn.Namespace, selectors...)
		if err != nil {
			return nil, fmt.Errorf("get pod %s fail: %w", nn, err)
		}

		if len(podList.Items) == 0 {
			return nil, fmt.Errorf("no Pods found for label selectors %+v", selectors)
		}

		pods = podList.Items
	}

	podsNamespacedNames := make([]types.NamespacedName, 0, len(pods))
	for i := range pods {
		podNsName := utils.NamespacedName(&pods[i])
		if pods[i].Status.Phase != "Running" {
			return podsNamespacedNames, fmt.Errorf("pod %s is not running", podNsName)
		}

		podsNamespacedNames = append(podsNamespacedNames, podNsName)
	}

	return podsNamespacedNames, nil
}

func extractGatewayConfigDump(fw kube.PortForwarder, includeAll bool) (map[string]interface{}, error) {
	out, err := gatewayConfigDumpRequest(fw.Address(), includeAll)
	if err != nil {
		return nil, err
	}

	var configDump map[string]interface{}
	if err := json.Unmarshal(out, &configDump); err != nil {
		return nil, err
	}

	maskSecretData(configDump, nil)
	return configDump, nil
}

func gatewayConfigDumpRequest(address string, includeAll bool) ([]byte, error) {
	url := fmt.Sprintf("http://%s%s", address, envoyGatewayConfigDumpPath)
	if includeAll {
		url = fmt.Sprintf("%s?resource=all", url)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	return io.ReadAll(resp.Body)
}

func marshalGatewayConfigDump(configDump gatewayConfigDump, output string) ([]byte, error) {
	out, err := json.MarshalIndent(configDump, "", "  ")
	if output == "yaml" {
		return yaml.JSONToYAML(out)
	}

	return out, err
}

func maskSecretData(value interface{}, path []string) {
	switch typed := value.(type) {
	case map[string]interface{}:
		if isSecretMap(typed, path) {
			maskSecretFields(typed)
		}
		for key, item := range typed {
			maskSecretData(item, append(path, key))
		}
	case []interface{}:
		for _, item := range typed {
			maskSecretData(item, path)
		}
	}
}

func isSecretMap(value map[string]interface{}, path []string) bool {
	if kind, ok := value["kind"].(string); ok && strings.EqualFold(kind, "Secret") {
		return true
	}

	hasData := value["data"] != nil || value["stringData"] != nil
	if !hasData {
		return false
	}

	if _, ok := value["type"].(string); ok {
		return true
	}

	for _, segment := range path {
		if segment == "secrets" {
			return true
		}
	}

	return false
}

func maskSecretFields(value map[string]interface{}) {
	if data, ok := value["data"]; ok {
		value["data"] = redactSecretData(data)
	}
	if data, ok := value["stringData"]; ok {
		value["stringData"] = redactSecretData(data)
	}
}

func redactSecretData(value interface{}) interface{} {
	data, ok := value.(map[string]interface{})
	if !ok {
		return "<redacted>"
	}

	for key := range data {
		data[key] = "<redacted>"
	}

	return data
}
