#!/bin/bash
# Copyright 2024 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

out=$(kustomize build --load-restrictor=LoadRestrictionsNone "${REPO_ROOT}/test/kustomization" | sed -e "s|gcr.io/cs-test/|example.com/|g")

expected_file="${REPO_ROOT}/test/kustomization/expected.yaml"

if [[ "${UPDATE_EXPECTED_OUTPUT:-}" == "true" ]]; then
  echo "${out}" > "${expected_file}"
  exit 0
fi

diff "${expected_file}" <( echo "${out}" )
