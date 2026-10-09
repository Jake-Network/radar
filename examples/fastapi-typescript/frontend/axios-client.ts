import axios from 'axios';
import type { UserResponse } from './types';

const client = axios.create();
export async function loadName() {
    const response = await client.get<UserResponse>('/api/v1/users/current');
    return response.data.display_name;
}
