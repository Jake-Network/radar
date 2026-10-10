# Two C++ agents, one broken build

Run `bash examples/cpp-cmake/demo.sh` from the Radar repository. Requires Git,
Go (or `RADAR_BIN=/absolute/path/radar`), Python 3, CMake 3.21+ with CTest,
make and a C++ compiler.

A CMake project has an `Inventory` class in `include/shop/inventory.h` and
`src/inventory.cpp`. One agent renames `Inventory::reserve` to `hold` and
updates every caller it knows about. Another agent adds `restock.h`,
`restock.cpp` and a test; `restock.cpp` calls `reserve`. Each branch builds
and passes its tests. Git merges them without a conflict, but the combined
tree does not compile.

Radar links the tests to the changed sources through `#include` edges and the
`restock.h`/`restock.cpp` naming pairing (both inferred). `radar gate --run`
then configures, builds and tests the project in the private candidate with
one command, `ctest --build-and-test . build/radar-ctest ...`, and reads the
JUnit report CTest writes. On the combined tree it reports `build_failed` at
`src/restock.cpp`, naming `reserve`.

The project has no dependencies, so nothing is downloaded. If CMake, CTest,
make or a compiler is missing, the demo prints `SKIPPED` instead of a result.
