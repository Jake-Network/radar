from pydantic import BaseModel


class Dataset(BaseModel):
    id: str
    organization_id: str
    rows: int


class ExportSummary(BaseModel):
    dataset_id: str
    total: int


class ExportRequest(BaseModel):
    dataset_id: str


class ExportJob(BaseModel):
    id: str
    organization_id: str
    dataset_id: str
    status: str
