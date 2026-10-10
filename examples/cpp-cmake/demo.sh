#!/usr/bin/env bash
# Two C++ branches that each build and pass, and do not compile together.
# Requires Git, CMake 3.21+ (with CTest), make and a C++ compiler. Nothing is
# downloaded: the project has no dependencies.
# Usage: demo.sh [PATH_TO_RADAR]
# RADAR_DEMO_REQUIRE=1 turns a skip into a failure (CI).
set -euo pipefail
skip() {
  echo "SKIPPED: $*"
  [[ "${RADAR_DEMO_REQUIRE:-}" == 1 ]] && exit 1
  exit 0
}
for tool in git cmake ctest make c++ python3; do
  command -v "$tool" >/dev/null || skip "$tool is not on PATH; the C++ demo did not run."
done
workspace=$(mktemp -d "${TMPDIR:-/tmp}/radar-cpp-demo.XXXXXX")
trap 'rm -rf "$workspace"' EXIT
repo="$workspace/checkout"
mkdir "$repo"
if [[ -n "${1:-${RADAR_BIN:-}}" ]]; then
  radar_bin=$(realpath "${1:-$RADAR_BIN}")
else
  source_root=$(cd "$(dirname "$0")/../.." && pwd)
  radar_bin="$workspace/radar"
  (cd "$source_root" && go build -o "$radar_bin" ./cmd/radar)
fi
git -C "$repo" init -q
git -C "$repo" symbolic-ref HEAD refs/heads/main
git -C "$repo" config user.name 'Radar demo'
git -C "$repo" config user.email demo@radar.invalid
git -C "$repo" config core.hooksPath /dev/null
printf 'build/\n.radar/\n' > "$repo/.gitignore"
mkdir -p "$repo/include/shop" "$repo/src" "$repo/tests"
# The library and the tests are globbed, so neither branch edits CMakeLists.txt.
cat > "$repo/CMakeLists.txt" <<'EOF'
cmake_minimum_required(VERSION 3.21)
project(shop CXX)
set(CMAKE_CXX_STANDARD 17)
enable_testing()
file(GLOB SHOP_SOURCES CONFIGURE_DEPENDS src/*.cpp)
add_library(shop STATIC ${SHOP_SOURCES})
target_include_directories(shop PUBLIC include)
file(GLOB SHOP_TESTS CONFIGURE_DEPENDS tests/*_test.cpp)
foreach(test_source ${SHOP_TESTS})
  get_filename_component(test_name ${test_source} NAME_WE)
  add_executable(${test_name} ${test_source})
  target_link_libraries(${test_name} shop)
  add_test(NAME ${test_name} COMMAND ${test_name})
endforeach()
EOF
cat > "$repo/include/shop/inventory.h" <<'EOF'
#pragma once

namespace shop {

class Inventory {
 public:
  explicit Inventory(int stock) : stock_(stock) {}
  int reserve(int count);

 private:
  int stock_;
};

}  // namespace shop
EOF
cat > "$repo/src/inventory.cpp" <<'EOF'
#include "shop/inventory.h"

#include <algorithm>

int shop::Inventory::reserve(int count) {
  int taken = std::min(count, stock_);
  stock_ -= taken;
  return taken;
}
EOF
cat > "$repo/tests/inventory_test.cpp" <<'EOF'
#include <shop/inventory.h>

int main() {
  shop::Inventory inventory(2);
  return inventory.reserve(5) == 2 ? 0 : 1;
}
EOF
git -C "$repo" add .
git -C "$repo" commit -qm baseline

git -C "$repo" checkout -qb agent-rename
sed -i 's/int reserve(int count)/int hold(int count)/' "$repo/include/shop/inventory.h"
sed -i 's/Inventory::reserve(/Inventory::hold(/' "$repo/src/inventory.cpp"
sed -i 's/\.reserve(/.hold(/' "$repo/tests/inventory_test.cpp"
git -C "$repo" commit -qam 'Rename Inventory::reserve to hold'
git -C "$repo" checkout -qb agent-restock main
cat > "$repo/include/shop/restock.h" <<'EOF'
#pragma once

#include "shop/inventory.h"

namespace shop {
int drain(Inventory& inventory);
}
EOF
cat > "$repo/src/restock.cpp" <<'EOF'
#include "shop/restock.h"

int shop::drain(Inventory& inventory) {
  return inventory.reserve(1 << 30);
}
EOF
cat > "$repo/tests/restock_test.cpp" <<'EOF'
#include <shop/restock.h>

int main() {
  shop::Inventory inventory(4);
  return shop::drain(inventory) == 4 ? 0 : 1;
}
EOF
git -C "$repo" add .
git -C "$repo" commit -qm 'Add restock draining'
git -C "$repo" checkout -q main

for branch in agent-rename agent-restock; do
  "$radar_bin" gate --root "$repo" --base main --run --json "$branch" > "$workspace/$branch.json"
done
set +e
"$radar_bin" gate --root "$repo" --base main --run --json agent-rename agent-restock > "$workspace/combined.json"
combined=$?
set -e
[[ "$combined" == 1 ]] || { echo "Expected the combined branches to fail (exit 1), got $combined" >&2; cat "$workspace/combined.json" >&2; exit 1; }
python3 - "$workspace" <<'PY'
import json, sys
from pathlib import Path
root = Path(sys.argv[1])
def executions(name):
    report = json.loads((root / (name + '.json')).read_text())
    return report['integration']['executions'] if 'integration' in report else report['executions']
for branch in ['agent-rename', 'agent-restock']:
    runs = executions(branch)
    assert len(runs) == 1 and runs[0]['status'] == 'passed' and runs[0]['observation']['tests_run'] > 0, (branch, runs)
    assert runs[0]['observation'].get('harness') == 'junit' and runs[0]['command'][:2] == ['ctest', '--build-and-test'], runs
failed = [r for r in executions('combined') if r['status'] == 'failed']
assert len(failed) == 1, executions('combined')
diagnosis = failed[0]['observation']['diagnosis']
assert diagnosis['kind'] == 'build_failed' and diagnosis['name'] == 'reserve', diagnosis
locations = {l['path'] for l in failed[0]['observation']['locations']}
assert locations == {'src/restock.cpp'}, locations
print('PASS: each C++ branch builds and passes alone; together the build fails where restock.cpp still calls reserve.')
print('Cause:', diagnosis['message'])
PY
[[ -z "$(git -C "$repo" status --porcelain)" ]]
