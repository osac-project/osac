/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package jq

type Variable struct {
	name  string
	value any
}

func String(name, value string) Variable {
	return Variable{
		name:  name,
		value: value,
	}
}

func Int(name string, value int) Variable {
	return Variable{
		name:  name,
		value: value,
	}
}

func Any(name string, value any) Variable {
	return Variable{
		name:  name,
		value: value,
	}
}
