# FastAPI / TypeScript discovery slice

From a separate Git checkout of this directory (so paths are repository-relative):

```sh
git init && git add . && git -c user.name=Example -c user.email=example@example.invalid commit -m baseline
radar discover --json > /tmp/discovery.json
radar check --base HEAD --suite full --json
python3 -m unittest test_contract
node --test frontend/contract.test.mjs
```

The application is representative source for static discovery; Radar never
imports it. Running FastAPI or TypeScript needs their normal application setup.
The fixture's Python and Node contract snapshot tests use standard libraries.
Ground truth: GET `/api/v1/users/current` has two consumers, typed fetch in
`frontend/client.ts` and an Axios instance in `frontend/axios-client.ts`.
Aliased relative Python imports and inherited/nested fields resolve to
`backend/models.py`; TypeScript aliases traverse a type barrel to `types/user.ts`.
The dynamic URL and same-named unrelated type are negative cases.

Discovery proposes relationships with unresolved deployment origin. It never
writes declarations. Python model pointers are source facts, not JSON Schema
contracts. After review, maintain a checked-in JSON/OpenAPI snapshot of the
actual response schema and accept the corresponding binding explicitly:

```sh
mkdir -p .radar
cp accepted-manifest.json .radar/contracts.json
git add .radar/contracts.json schema.json
git -c user.name=Example -c user.email=example@example.invalid commit -m 'Accept reviewed response obligations'
```

The schema snapshot above was manually reviewed; it is not generated proof of
runtime serialization. Removing `profile.email` from both the Python model and
reviewed JSON schema and committing the change then yields an inferred discovery warning and an
authoritative declared-contract failure. Changing only Python remains inferred
until the accepted schema or observed runtime test establishes the obligation.
Dynamic schemas, validators and middleware still require runtime verification.

Opt-in actual FastAPI/Pydantic qualification (prepared Python environment):

```sh
python3 -m venv /tmp/radar-example-env
/tmp/radar-example-env/bin/python -m pip install -r backend/requirements.txt
/tmp/radar-example-env/bin/python -m unittest test_contract test_fastapi_runtime
/tmp/radar-example-env/bin/python verify-example.py /absolute/path/to/radar
```

Or, with uv, from the Radar checkout:

```sh
uv run --no-project --with fastapi==0.115.0 --with pydantic==2.9.2 --with httpx==0.27.2 \
  python examples/fastapi-typescript/verify-example.py /absolute/path/to/radar
```

`verify-example.py` explicitly executes tests on a fresh committed fixture. It
requires a passing full selection/execution gate and three recognized tests:
real FastAPI TestClient/Pydantic response, Python schema snapshot, and Node schema
consumer snapshot. This exact pinned slice passed locally. This does not execute
the TypeScript/Axios client against a deployed application or certify serializers,
validators or framework versions beyond those tested. Dependency downloads belong
to explicit environment preparation; Radar discovery never performs them.
