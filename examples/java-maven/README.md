# Two Java agents, one broken build

Run `bash examples/java-maven/demo.sh` from the Radar repository. Requires Git,
Go (or `RADAR_BIN=/absolute/path/radar`), Python 3, a JDK 17+ and Maven.

A Maven reactor has a `core` module with `Inventory` and an `app` module that
calls it. One agent renames `Inventory.reserve` to `hold` and updates every
caller it knows about. Another agent adds `Restock`, which calls `reserve`.
Each branch builds and passes its tests. Git merges them without a conflict,
but the combined tree does not compile.

`radar gate --run` selects the changed and affected test classes, runs each
module from the reactor root (`mvn -o -B -pl app -am -Dtest=... test`) and
reads Surefire's JUnit reports. On the combined tree it reports `build_failed`
at `app/src/main/java/com/shop/app/Restock.java`, naming `reserve`.

Radar runs Maven offline and never downloads anything. When the local Maven
repository lacks JUnit 5 or the default plugins, the demo prints `SKIPPED`
instead of a result. Run it once with `--prime` to let Maven fill its cache
online, or set `MAVEN_REPO_LOCAL` to use a different repository. If `java` or
`mvn` is not installed, the demo is skipped too.
