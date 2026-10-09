from fastapi import APIRouter, HTTPException
from .auth import Principal, require_organization
from .jobs import enqueue_export
from .models import Dataset, ExportJob, ExportRequest, ExportSummary

router = APIRouter()
datasets: dict[str, Dataset] = {}


def authorized_dataset(principal: Principal, organization_id: str, dataset_id: str) -> Dataset:
    require_organization(principal, organization_id)
    dataset = datasets.get(dataset_id)
    if dataset is None or dataset.organization_id != organization_id:
        raise HTTPException(status_code=404, detail="Dataset not found")
    return dataset


@router.get("/organizations/{organization_id}/datasets/{dataset_id}/summary",
            response_model=ExportSummary)
def dataset_summary(organization_id: str, dataset_id: str, principal: Principal) -> ExportSummary:
    dataset = authorized_dataset(principal, organization_id, dataset_id)
    return ExportSummary(dataset_id=dataset.id, total=dataset.rows)


@router.post("/organizations/{organization_id}/exports", response_model=ExportJob)
def create_export(organization_id: str, request: ExportRequest, principal: Principal) -> ExportJob:
    authorized_dataset(principal, organization_id, request.dataset_id)
    return enqueue_export(organization_id, request.dataset_id)
