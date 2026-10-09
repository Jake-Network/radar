# Selection evaluation and gate-safety demo

* `demo.sh RADAR` shows that deleting the contract manifest cannot pass a
  contract gate, that an explicit retirement can, and that targeted, grouped
  suites observe real failures in the private candidate.
* `evaluate.py` runs the labeled mutation evaluation described in
  [docs/VALIDATION_SELECTION.md](../../docs/VALIDATION_SELECTION.md).
  `mutations.json` targets `fixture/`, and `click-mutations.json` targets a
  prepared Pallets Click checkout. `results/` holds the published run.

The fixture is a small, realistic monorepo, not production-scale evidence.
