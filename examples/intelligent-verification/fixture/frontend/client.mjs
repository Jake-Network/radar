import { quantity } from './cart.mjs';

export async function checkoutTotal(baseURL) {
  const response = await fetch(`${baseURL}/products/demo-product`);
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  const product = await response.json();
  return product.unit_price * quantity;
}

if (process.argv[2]) {
  console.log(await checkoutTotal(process.argv[2]));
}
