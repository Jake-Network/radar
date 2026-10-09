"""Order summaries returned by GET /orders/{id}/summary."""
from app.pricing import apply_discount, subtotal


def summarize(order_id, lines, discount_percent=0, currency="USD"):
    total = apply_discount(subtotal(lines), discount_percent)
    return {
        "id": order_id,
        "total": total,
        "currency": currency,
        "items": sum(qty for _, qty in lines),
    }
