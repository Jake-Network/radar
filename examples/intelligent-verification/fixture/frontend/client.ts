import type { Product } from './types';

// Typed counterpart of the executable Node client. The demo does not typecheck.
export async function product(baseURL: string): Promise<Product> {
  const response = await fetch(`${baseURL}/products/demo-product`);
  return response.json() as Promise<Product>;
}

export function total(product: Product, quantity: number): number {
  return product.unit_price * quantity;
}
