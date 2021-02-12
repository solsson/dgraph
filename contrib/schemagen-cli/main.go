/*
 * Copyright 2021 Dgraph Labs, Inc. and Contributors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	dschema "github.com/dgraph-io/dgraph/graphql/schema"
)

var (
	source   string
	generate string
)

func init() {
	flag.StringVar(&source, "source", "-", "source path; required; stdin is TODO")
	flag.StringVar(&generate, "generate", "graphql", "Output type, graphql or dgraph")
	flag.Parse()
}

func main() {
	if source == "-" {
		flag.PrintDefaults()
		os.Exit(1)
	}
	schema, err := ioutil.ReadFile(source)
	if err != nil {
		cwd, _ := os.Getwd()
		panic(fmt.Errorf("Failed to read source %s (from %s): %w", source, cwd, err))
	}
	handler, err := dschema.NewHandler(string(schema), false)
	if err != nil {
		panic(fmt.Errorf("Failed to init for length %d: %w", len(schema), err))
	}
	var result string
	if generate == "graphql" {
		result = handler.GQLSchema()
	} else if generate == "dgraph" {
		result = handler.DGSchema()
	} else {
		panic(fmt.Errorf("Invalid output type: %s", generate))
	}
	fmt.Print(result)
}
