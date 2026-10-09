from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI()

class User(BaseModel):
    id: int
    email: str

@app.get('/users', response_model=User)
def users():
    return User(id=1, email='developer@example.com')
