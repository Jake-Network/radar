# Static adapter example; the offline runtime uses the service's JSON directly.
from pydantic import BaseModel


class Product(BaseModel):
    sku: str
    unit_price: int
    currency: str
