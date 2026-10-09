import type { UserResponse as CurrentUser } from './types';

export async function loadUser() {
    const user: CurrentUser = await (await fetch('/api/v1/users/current')).json();
    return user.profile.email;
}
