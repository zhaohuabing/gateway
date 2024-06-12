// Copyright Envoy Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tests

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/archive"
)

func TestE2E(t *testing.T) {
	err := pushWasmImageForTest("localhost:5000")
	if err != nil {
		t.Fatal(err)

	}
}

func pushWasmImageForTest(gwAddr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*120)
	defer cancel()

	var (
		cli *client.Client
		tar io.Reader
		res dockertypes.ImageBuildResponse
		rd  io.ReadCloser
		err error
	)

	tag := fmt.Sprintf("%s/testwasm:v1.0.0", gwAddr)

	if cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation()); err != nil {
		return err
	}

	if tar, err = archive.TarWithOptions("../testdata/wasm/", &archive.TarOptions{}); err != nil {
		return err
	}

	opts := dockertypes.ImageBuildOptions{
		Dockerfile: "Dockerfile",
		Tags:       []string{tag},
		Remove:     true,
	}
	if res, err = cli.ImageBuild(ctx, tar, opts); err != nil {
		return err
	}

	defer func() { _ = res.Body.Close() }()

	if err = printDockerCLIResponse(res.Body); err != nil {
		return err
	}

	if rd, err = cli.ImagePush(ctx, tag, image.PushOptions{
		RegistryAuth: "none",
	}); err != nil {
		return err
	}

	defer func() {
		_ = rd.Close()
	}()

	if err = printDockerCLIResponse(rd); err != nil {
		return err
	}
	return nil
}

type ErrorLine struct {
	Error       string      `json:"error"`
	ErrorDetail ErrorDetail `json:"errorDetail"`
}

type ErrorDetail struct {
	Message string `json:"message"`
}

func printDockerCLIResponse(rd io.Reader) error {
	var lastLine string

	scanner := bufio.NewScanner(rd)
	for scanner.Scan() {
		lastLine = scanner.Text()
		fmt.Println(scanner.Text())
	}

	errLine := &ErrorLine{}
	_ = json.Unmarshal([]byte(lastLine), errLine)
	if errLine.Error != "" {
		return errors.New(errLine.Error)
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}
