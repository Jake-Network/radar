# FastAPI adapter source is indexed, but not executed by the offline demo.
from fastapi import FastAPI
from .models import Product
from .service import product_response

app = FastAPI()


@app.get("/products/demo-product", response_model=Product)
def product():
    return product_response()
