"""Warehouse stock reservations."""


class Stock:
    def __init__(self, levels):
        self.levels = dict(levels)

    def reserve(self, sku, quantity):
        available = self.levels.get(sku, 0)
        if quantity > available:
            return False
        self.levels[sku] = available - quantity
        return True
