// k6-load-test.js
//
// Grafana K6 Load Test for Observability Example Application
//
// Run with:
//   k6 run k6-load-test.js
//
// Run with custom options:
//   k6 run --vus 50 --duration 5m k6-load-test.js
//
// Run with stages (ramp-up/down):
//   k6 run --stage 1m:10,3m:50,1m:0 k6-load-test.js

import http from 'k6/http';
import { check, sleep, group } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';

// ============================================================================
// Configuration
// ============================================================================

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

// Custom metrics
const httpReqFailed = new Rate('http_req_failed_custom');
const cacheHitRate = new Rate('cache_hit_rate');
const dbOperations = new Counter('db_operations');
const errorRate = new Rate('error_rate');
const slowRequests = new Counter('slow_requests');

// ============================================================================
// Test Configuration
// ============================================================================

export const options = {
  // Test stages - simula traffico realistico
  stages: [
    { duration: '30s', target: 10 },  // Warm-up: ramp to 10 users
    { duration: '1m', target: 20 },   // Normal load: 20 users
    { duration: '2m', target: 50 },   // Peak load: 50 users
    { duration: '1m', target: 100 },  // Stress test: 100 users
    { duration: '1m', target: 50 },   // Scale down: 50 users
    { duration: '30s', target: 0 },   // Cool down: 0 users
  ],

  // Thresholds - definisce i criteri di successo del test
  thresholds: {
    // HTTP metrics
    'http_req_duration': ['p(95)<500', 'p(99)<1000'], // 95% requests < 500ms, 99% < 1s
    'http_req_failed': ['rate<0.05'],                  // Less than 5% errors
    'http_req_duration{endpoint:health}': ['p(95)<100'], // Health check very fast
    
    // Custom metrics
    'http_req_failed_custom': ['rate<0.05'],
    'error_rate': ['rate<0.10'],                       // Less than 10% errors overall
    'cache_hit_rate': ['rate>0.50'],                   // Cache hit rate > 50%
  },

  // Tags for all requests
  tags: {
    test_name: 'observability_load_test',
  },
};

// ============================================================================
// Setup - runs once before test
// ============================================================================

export function setup() {
  console.log('Starting load test against:', BASE_URL);
  
  // Check if service is ready
  const healthRes = http.get(`${BASE_URL}/health`);
  if (healthRes.status !== 200) {
    throw new Error('Service not ready!');
  }
  
  const readyRes = http.get(`${BASE_URL}/ready`);
  if (readyRes.status !== 200) {
    throw new Error('Service not ready (observability not initialized)!');
  }
  
  console.log('Service is ready, starting load test...');
  
  return { startTime: new Date() };
}

// ============================================================================
// Main Test Function - runs for each VU iteration
// ============================================================================

export default function(data) {
  // Weighted distribution of operations
  const rand = Math.random();
  
  if (rand < 0.30) {
    // 30% - Database operations (reads)
    testDatabaseReads();
  } else if (rand < 0.45) {
    // 15% - Database operations (writes)
    testDatabaseWrites();
  } else if (rand < 0.70) {
    // 25% - Cache operations
    testCacheOperations();
  } else if (rand < 0.90) {
    // 20% - Mixed operations
    testMixedOperations();
  } else if (rand < 0.97) {
    // 7% - Slow operations
    testSlowOperations();
  } else {
    // 3% - Random operations (can fail)
    testRandomOperations();
  }
  
  // Think time - simula pausa tra richieste dell'utente
  sleep(Math.random() * 2 + 0.5); // 0.5-2.5 seconds
}

// ============================================================================
// Test Scenarios
// ============================================================================

function testDatabaseReads() {
  group('Database Reads', function() {
    // List users
    let res = http.get(`${BASE_URL}/api/users`, {
      tags: { endpoint: 'list_users', operation: 'db_read' },
    });
    
    check(res, {
      'list users: status 200': (r) => r.status === 200,
      'list users: has users array': (r) => r.json('users') !== undefined,
    });
    
    httpReqFailed.add(res.status !== 200);
    dbOperations.add(1);
    
    // Get specific user
    const userId = Math.floor(Math.random() * 1000) + 1;
    res = http.get(`${BASE_URL}/api/users/${userId}`, {
      tags: { endpoint: 'get_user', operation: 'db_read' },
    });
    
    check(res, {
      'get user: status 200': (r) => r.status === 200,
      'get user: has user id': (r) => r.json('id') !== undefined,
    });
    
    httpReqFailed.add(res.status !== 200);
    dbOperations.add(1);
  });
}

function testDatabaseWrites() {
  group('Database Writes', function() {
    const operations = ['create', 'update', 'delete'];
    const operation = operations[Math.floor(Math.random() * operations.length)];
    
    if (operation === 'create') {
      // Create user
      const payload = JSON.stringify({
        username: `user_${Date.now()}_${Math.random()}`,
        email: `test${Date.now()}@example.com`,
      });
      
      const res = http.post(`${BASE_URL}/api/users`, payload, {
        headers: { 'Content-Type': 'application/json' },
        tags: { endpoint: 'create_user', operation: 'db_write' },
      });
      
      check(res, {
        'create user: status 201': (r) => r.status === 201,
        'create user: has id': (r) => r.json('id') !== undefined,
      });
      
      httpReqFailed.add(res.status !== 201);
      dbOperations.add(1);
      
    } else if (operation === 'update') {
      // Update user
      const userId = Math.floor(Math.random() * 1000) + 1;
      const payload = JSON.stringify({
        username: `updated_user_${Date.now()}`,
      });
      
      const res = http.put(`${BASE_URL}/api/users/${userId}`, payload, {
        headers: { 'Content-Type': 'application/json' },
        tags: { endpoint: 'update_user', operation: 'db_write' },
      });
      
      check(res, {
        'update user: status 200': (r) => r.status === 200,
      });
      
      httpReqFailed.add(res.status !== 200);
      dbOperations.add(1);
      
    } else {
      // Delete user
      const userId = Math.floor(Math.random() * 1000) + 1;
      
      const res = http.del(`${BASE_URL}/api/users/${userId}`, null, {
        tags: { endpoint: 'delete_user', operation: 'db_write' },
      });
      
      check(res, {
        'delete user: status 200': (r) => r.status === 200,
      });
      
      httpReqFailed.add(res.status !== 200);
      dbOperations.add(1);
    }
  });
}

function testCacheOperations() {
  group('Cache Operations', function() {
    const cacheKey = `key_${Math.floor(Math.random() * 100)}`;
    
    // Try to get from cache
    let res = http.get(`${BASE_URL}/api/cache/${cacheKey}`, {
      tags: { endpoint: 'cache_get', operation: 'cache' },
    });
    
    const isHit = res.status === 200 && res.json('hit') === true;
    
    check(res, {
      'cache get: valid status': (r) => r.status === 200 || r.status === 404,
    });
    
    cacheHitRate.add(isHit);
    httpReqFailed.add(res.status !== 200 && res.status !== 404);
    
    // If miss, set the value
    if (!isHit) {
      const payload = JSON.stringify({
        value: `cached_value_${Date.now()}`,
        ttl: 300,
      });
      
      res = http.post(`${BASE_URL}/api/cache/${cacheKey}`, payload, {
        headers: { 'Content-Type': 'application/json' },
        tags: { endpoint: 'cache_set', operation: 'cache' },
      });
      
      check(res, {
        'cache set: status 200': (r) => r.status === 200,
        'cache set: success': (r) => r.json('success') === true,
      });
      
      httpReqFailed.add(res.status !== 200);
    }
    
    // Occasionally delete
    if (Math.random() < 0.2) {
      res = http.del(`${BASE_URL}/api/cache/${cacheKey}`, null, {
        tags: { endpoint: 'cache_delete', operation: 'cache' },
      });
      
      check(res, {
        'cache delete: valid status': (r) => r.status === 200 || r.status === 500,
      });
      
      httpReqFailed.add(res.status !== 200 && res.status !== 500);
    }
  });
}

function testMixedOperations() {
  group('Mixed Operations', function() {
    // Sequential operations simulating a user flow
    
    // 1. List users
    let res = http.get(`${BASE_URL}/api/users`, {
      tags: { endpoint: 'list_users', scenario: 'mixed' },
    });
    check(res, { 'mixed: list users ok': (r) => r.status === 200 });
    httpReqFailed.add(res.status !== 200);
    
    sleep(0.5);
    
    // 2. Get specific user
    const userId = Math.floor(Math.random() * 1000) + 1;
    res = http.get(`${BASE_URL}/api/users/${userId}`, {
      tags: { endpoint: 'get_user', scenario: 'mixed' },
    });
    check(res, { 'mixed: get user ok': (r) => r.status === 200 });
    httpReqFailed.add(res.status !== 200);
    
    sleep(0.3);
    
    // 3. Check cache
    const cacheKey = `user_${userId}`;
    res = http.get(`${BASE_URL}/api/cache/${cacheKey}`, {
      tags: { endpoint: 'cache_get', scenario: 'mixed' },
    });
    check(res, { 'mixed: cache get ok': (r) => r.status === 200 || r.status === 404 });
    cacheHitRate.add(res.status === 200 && res.json('hit') === true);
  });
}

function testSlowOperations() {
  group('Slow Operations', function() {
    const start = Date.now();
    
    const res = http.get(`${BASE_URL}/api/slow`, {
      tags: { endpoint: 'slow', operation: 'slow' },
      timeout: '10s', // Increase timeout for slow endpoint
    });
    
    const duration = Date.now() - start;
    
    check(res, {
      'slow: status 200': (r) => r.status === 200,
      'slow: duration > 2s': () => duration > 2000,
    });
    
    httpReqFailed.add(res.status !== 200);
    slowRequests.add(1);
    
    if (duration > 2000) {
      console.log(`Slow request detected: ${duration}ms`);
    }
  });
}

function testRandomOperations() {
  group('Random Operations', function() {
    const res = http.get(`${BASE_URL}/api/random`, {
      tags: { endpoint: 'random', operation: 'random' },
    });
    
    const success = res.status === 200;
    
    check(res, {
      'random: has response': (r) => r.status === 200 || r.status === 500,
    });
    
    httpReqFailed.add(!success);
    errorRate.add(!success);
    
    if (!success) {
      console.log('Random error occurred (expected behavior)');
    }
  });
}

// ============================================================================
// Health Check Scenario - runs in parallel
// ============================================================================

export function healthCheck() {
  const res = http.get(`${BASE_URL}/health`, {
    tags: { endpoint: 'health', scenario: 'health_check' },
  });
  
  check(res, {
    'health check: status 200': (r) => r.status === 200,
    'health check: response time < 100ms': (r) => r.timings.duration < 100,
  });
  
  sleep(5); // Check every 5 seconds
}

// ============================================================================
// Teardown - runs once after test
// ============================================================================

export function teardown(data) {
  const endTime = new Date();
  const duration = (endTime - data.startTime) / 1000;
  
  console.log('');
  console.log(' Load test completed!');
  console.log(`Total duration: ${duration.toFixed(2)}s`);
  console.log('');
  console.log('Check the metrics in your observability stack:');
  console.log('');
}

// ============================================================================
// Helper Functions
// ============================================================================

function randomString(length) {
  const chars = 'abcdefghijklmnopqrstuvwxyz0123456789';
  let result = '';
  for (let i = 0; i < length; i++) {
    result += chars.charAt(Math.floor(Math.random() * chars.length));
  }
  return result;
}