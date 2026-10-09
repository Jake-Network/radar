import type { User } from './types';
const user: User = await (await fetch('/users')).json();
console.log(user.email);
