from collections import deque
from uuid import uuid4
from .models import ExportJob

pending_jobs: deque[ExportJob] = deque()


def enqueue_export(organization_id: str, dataset_id: str) -> ExportJob:
    job = ExportJob(id=str(uuid4()), organization_id=organization_id,
                    dataset_id=dataset_id, status="pending")
    pending_jobs.append(job)
    return job
