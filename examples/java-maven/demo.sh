#!/usr/bin/env bash
# Two Java branches that each build and pass, and do not compile together.
# Requires Git, a JDK and Maven whose local repository already holds JUnit 5
# and the default Maven plugins: Radar runs Maven offline and never downloads.
# Usage: demo.sh [PATH_TO_RADAR] [--prime]
#   --prime  run `mvn -B test` once online on the baseline to fill the cache.
# MAVEN_REPO_LOCAL selects a Maven repository other than ~/.m2/repository.
# RADAR_DEMO_REQUIRE=1 turns a skip into a failure (CI).
set -euo pipefail
skip() {
  echo "SKIPPED: $*"
  [[ "${RADAR_DEMO_REQUIRE:-}" == 1 ]] && exit 1
  exit 0
}
prime=false
radar_arg=""
for arg in "$@"; do
  case "$arg" in
    --prime) prime=true ;;
    *) radar_arg="$arg" ;;
  esac
done
for tool in git java mvn; do
  command -v "$tool" >/dev/null || skip "$tool is not on PATH; the Java demo did not run."
done
workspace=$(mktemp -d "${TMPDIR:-/tmp}/radar-java-demo.XXXXXX")
trap 'rm -rf "$workspace"' EXIT
repo="$workspace/checkout"
mkdir "$repo"
if [[ -n "${radar_arg:-${RADAR_BIN:-}}" ]]; then
  radar_bin=$(realpath "${radar_arg:-$RADAR_BIN}")
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
printf 'target/\n.radar/\n' > "$repo/.gitignore"
if [[ -n "${MAVEN_REPO_LOCAL:-}" ]]; then
  mkdir -p "$repo/.mvn"
  printf -- '-Dmaven.repo.local=%s\n' "$MAVEN_REPO_LOCAL" > "$repo/.mvn/maven.config"
fi
module_pom() { # artifactId [dependency xml]
  cat <<EOF
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent><groupId>com.shop</groupId><artifactId>shop</artifactId><version>1.0</version></parent>
  <artifactId>$1</artifactId>
  <dependencies>$2
    <dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId><version>5.10.2</version><scope>test</scope></dependency>
  </dependencies>
</project>
EOF
}
cat > "$repo/pom.xml" <<'EOF'
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.shop</groupId>
  <artifactId>shop</artifactId>
  <version>1.0</version>
  <packaging>pom</packaging>
  <properties>
    <maven.compiler.release>17</maven.compiler.release>
    <project.build.sourceEncoding>UTF-8</project.build.sourceEncoding>
  </properties>
  <modules>
    <module>core</module>
    <module>app</module>
  </modules>
</project>
EOF
mkdir -p "$repo/core/src/main/java/com/shop" "$repo/core/src/test/java/com/shop" "$repo/app/src/main/java/com/shop/app" "$repo/app/src/test/java/com/shop/app"
module_pom core "" > "$repo/core/pom.xml"
module_pom app "
    <dependency><groupId>com.shop</groupId><artifactId>core</artifactId><version>1.0</version></dependency>" > "$repo/app/pom.xml"
cat > "$repo/core/src/main/java/com/shop/Inventory.java" <<'EOF'
package com.shop;

public class Inventory {
    private int stock;

    public Inventory(int stock) { this.stock = stock; }

    public int reserve(int count) {
        int taken = Math.min(count, stock);
        stock -= taken;
        return taken;
    }
}
EOF
cat > "$repo/core/src/test/java/com/shop/InventoryTest.java" <<'EOF'
package com.shop;

import static org.junit.jupiter.api.Assertions.assertEquals;
import org.junit.jupiter.api.Test;

class InventoryTest {
    @Test
    void takesAtMostTheStock() { assertEquals(2, new Inventory(2).reserve(5)); }
}
EOF
cat > "$repo/app/src/main/java/com/shop/app/Checkout.java" <<'EOF'
package com.shop.app;

import com.shop.Inventory;

public class Checkout {
    public static boolean buy(Inventory inventory, int count) { return inventory.reserve(count) == count; }
}
EOF
cat > "$repo/app/src/test/java/com/shop/app/CheckoutTest.java" <<'EOF'
package com.shop.app;

import static org.junit.jupiter.api.Assertions.assertTrue;
import com.shop.Inventory;
import org.junit.jupiter.api.Test;

class CheckoutTest {
    @Test
    void buysWhatIsInStock() { assertTrue(Checkout.buy(new Inventory(3), 2)); }
}
EOF
git -C "$repo" add .
git -C "$repo" commit -qm baseline
if $prime; then
  (cd "$repo" && mvn -B -q test >/dev/null)
  rm -rf "$repo"/*/target
fi
# The baseline must build offline, or the demo would only show a cache miss.
if ! (cd "$repo" && mvn -o -B -q test >"$workspace/preflight.txt" 2>&1); then
  skip "the baseline does not build with 'mvn -o' (Maven cache lacks JUnit or plugins). Run once with --prime (downloads into your Maven repository), then rerun."
fi
rm -rf "$repo"/*/target

git -C "$repo" checkout -qb agent-rename
sed -i 's/public int reserve(int count)/public int hold(int count)/' "$repo/core/src/main/java/com/shop/Inventory.java"
sed -i 's/\.reserve(/.hold(/' "$repo/app/src/main/java/com/shop/app/Checkout.java" "$repo/core/src/test/java/com/shop/InventoryTest.java"
git -C "$repo" commit -qam 'Rename Inventory.reserve to hold'
git -C "$repo" checkout -qb agent-restock main
cat > "$repo/app/src/main/java/com/shop/app/Restock.java" <<'EOF'
package com.shop.app;

import com.shop.Inventory;

public class Restock {
    public static int drain(Inventory inventory) { return inventory.reserve(Integer.MAX_VALUE); }
}
EOF
cat > "$repo/app/src/test/java/com/shop/app/RestockTest.java" <<'EOF'
package com.shop.app;

import static org.junit.jupiter.api.Assertions.assertEquals;
import com.shop.Inventory;
import org.junit.jupiter.api.Test;

class RestockTest {
    @Test
    void drainsEverything() { assertEquals(4, Restock.drain(new Inventory(4))); }
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
    assert runs and all(r['status'] == 'passed' and r['observation']['tests_run'] > 0 for r in runs), (branch, runs)
    assert all(r['observation'].get('harness') == 'junit' for r in runs), runs
failed = [r for r in executions('combined') if r['status'] == 'failed']
assert len(failed) == 1, executions('combined')
diagnosis = failed[0]['observation']['diagnosis']
assert diagnosis['kind'] == 'build_failed' and diagnosis['name'] == 'reserve', diagnosis
locations = {l['path'] for l in failed[0]['observation']['locations']}
assert 'app/src/main/java/com/shop/app/Restock.java' in locations, locations
print('PASS: each Java branch builds and passes alone; together the build fails where Restock still calls reserve.')
print('Cause:', diagnosis['message'])
PY
[[ -z "$(git -C "$repo" status --porcelain)" ]]
