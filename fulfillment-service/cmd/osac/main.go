/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/osac-project/fulfillment-service/internal/cmd/cli"
	"github.com/osac-project/fulfillment-service/internal/exit"
)

func main() {
	err := run()
	exitErr, ok := errors.AsType[exit.Error](err)
	if ok {
		os.Exit(exitErr.Code())
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := cli.Root()
	if err != nil {
		return err
	}
	return root.Execute()
}
