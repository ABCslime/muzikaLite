import client from './client'
import { API_URLS } from '@/utils/constants'

export const authAPI = {
  async login(username, password) {
    const response = await client.post(`${API_URLS.AUTH}/login`, {
      username,
      password,
    })
    return response.data
  },

  async register(username, password, email = null) {
    const response = await client.post(`${API_URLS.AUTH}/user`, {
      username,
      password,
      email,
    })
    return response.data
  },

  async deleteUser(userId) {
    await client.delete(`${API_URLS.AUTH}/user/${userId}`)
  },
}

