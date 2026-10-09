from fastapi import APIRouter as Router
from ..models import User as Response

router = Router(prefix="/users")

@router.get(
    "/current",
    response_model=Response,
)
def current_user():
    return {"id": 1, "profile": {"email": "reader@example.invalid"}, "display_name": "Reader"}
