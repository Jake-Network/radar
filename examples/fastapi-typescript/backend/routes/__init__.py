from fastapi import APIRouter
from .users import router as users

router = APIRouter(prefix="/v1")
router.include_router(users)
