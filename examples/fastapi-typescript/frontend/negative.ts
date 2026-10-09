import type { User } from './other/user';
import type { UserResponse } from './types';
// Same name on a different literal route is not the backend's endpoint.
const unrelated: User = await (await fetch('/unrelated')).json();
console.log(unrelated.unrelated);
// Dynamic URL cannot establish a producer relationship.
declare const configuredURL: string;
const dynamic: UserResponse = await (await fetch(configuredURL)).json();
console.log(dynamic.display_name);
