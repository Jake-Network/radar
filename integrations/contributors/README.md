# Detector contribution drafts

Generate 15 concrete local issue drafts:

```sh
python3 integrations/contributors/detector_issues.py --out /tmp/radar-detector-issues
python3 -m unittest discover -s integrations/contributors
```

Each draft names an existing owner package, bounded supported syntax, positive
and negative fixtures, base-existing and single-branch cases, unknown behavior,
and repair guidance. The generator refuses to overwrite existing drafts.
Review current support before publishing: these are candidate tasks, not claims
that each feature is currently absent. Discovery additions remain proposals;
they cannot silently become hard gate checks. A maintainer must confirm a task
is small and unclaimed before assigning `good first issue`.

The generator writes no GitHub issues and performs no network requests. See
[the contribution guide](../../CONTRIBUTING.md#add-a-detector) for implementation
and verification requirements.
