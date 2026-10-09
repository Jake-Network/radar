from dataclasses import dataclass
from fastapi import HTTPException


@dataclass
class Principal:
    user_id: str
    organization_ids: set[str]


def require_organization(principal: Principal, organization_id: str) -> None:
    if organization_id not in principal.organization_ids:
        raise HTTPException(status_code=403, detail="Organization access denied")
