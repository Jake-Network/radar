from fastapi import FastAPI
from .routes import router as api

app = FastAPI()
app.include_router(api, prefix="/api")
