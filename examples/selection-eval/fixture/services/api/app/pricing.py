"""Price arithmetic in integer cents."""


def line_total(unit_cents, quantity):
    if quantity < 0:
        raise ValueError("quantity must be non-negative")
    return unit_cents * quantity


def apply_discount(cents, percent):
    if not 0 <= percent <= 100:
        raise ValueError("percent out of range")
    return cents - (cents * percent + 50) // 100


def subtotal(lines):
    return sum(line_total(unit, qty) for unit, qty in lines)
