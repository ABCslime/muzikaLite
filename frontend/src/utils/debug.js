import { API_URLS } from './constants'
import client from '@/api/client'

export async function testServiceConnections() {
  const results = {
    auth: { connected: false, status: null, error: null },
    playlist: { connected: false, status: null, error: null },
    queue: { connected: false, status: null, error: null },
  }

  console.group('🔍 Testing Backend Service Connections')
  console.log('API URLs:', API_URLS)

  // Test AuthorizationManager (port 8091)
  try {
    console.log('\n📡 Testing AuthorizationManager (8091)...')
    const authResponse = await client.get(`${API_URLS.AUTH}/login`, {
      validateStatus: () => true, // Don't throw on any status
    })
    results.auth = {
      connected: true,
      status: authResponse.status,
      error: null,
    }
    console.log(`✅ AuthorizationManager: Connected (Status: ${authResponse.status})`)
  } catch (error) {
    results.auth = {
      connected: false,
      status: error.response?.status || null,
      error: error.message,
    }
    console.error(`❌ AuthorizationManager: Failed - ${error.message}`)
    if (error.response) {
      console.error(`   Status: ${error.response.status}`)
      console.error(`   Data:`, error.response.data)
    }
  }

  // Test PlaylistManager (port 8092)
  try {
    console.log('\n📡 Testing PlaylistManager (8092)...')
    const playlistResponse = await client.get(`${API_URLS.PLAYLIST}/playlist`, {
      validateStatus: () => true,
    })
    results.playlist = {
      connected: true,
      status: playlistResponse.status,
      error: null,
    }
    console.log(`✅ PlaylistManager: Connected (Status: ${playlistResponse.status})`)
  } catch (error) {
    results.playlist = {
      connected: false,
      status: error.response?.status || null,
      error: error.message,
    }
    console.error(`❌ PlaylistManager: Failed - ${error.message}`)
    if (error.response) {
      console.error(`   Status: ${error.response.status}`)
      console.error(`   Data:`, error.response.data)
    }
  }

  // Test QueueManager (port 8090)
  try {
    console.log('\n📡 Testing QueueManager (8090)...')
    const queueResponse = await client.get(`${API_URLS.QUEUE}/queue`, {
      validateStatus: () => true,
    })
    results.queue = {
      connected: true,
      status: queueResponse.status,
      error: null,
    }
    console.log(`✅ QueueManager: Connected (Status: ${queueResponse.status})`)
  } catch (error) {
    results.queue = {
      connected: false,
      status: error.response?.status || null,
      error: error.message,
    }
    console.error(`❌ QueueManager: Failed - ${error.message}`)
    if (error.response) {
      console.error(`   Status: ${error.response.status}`)
      console.error(`   Data:`, error.response.data)
    }
  }

  console.log('\n📊 Connection Summary:')
  console.table({
    'AuthorizationManager (8091)': {
      Connected: results.auth.connected ? '✅ Yes' : '❌ No',
      Status: results.auth.status || 'N/A',
      Error: results.auth.error || 'None',
    },
    'PlaylistManager (8092)': {
      Connected: results.playlist.connected ? '✅ Yes' : '❌ No',
      Status: results.playlist.status || 'N/A',
      Error: results.playlist.error || 'None',
    },
    'QueueManager (8090)': {
      Connected: results.queue.connected ? '✅ Yes' : '❌ No',
      Status: results.queue.status || 'N/A',
      Error: results.queue.error || 'None',
    },
  })

  console.groupEnd()

  return results
}

