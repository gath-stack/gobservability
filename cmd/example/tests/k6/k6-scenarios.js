// k6-scenarios.js
//
// Advanced K6 Load Test with Multiple Scenarios
//
// This version uses K6 scenarios for more complex testing patterns
//
// Run with:
//   k6 run k6-scenarios.js
//
// Run with custom duration:
//   k6 run -e DURATION=10m k6-scenarios.js

import http from 'k6/http';
import { check, sleep, group } from 'k6';
import { Counter, Rate, Trend, Gauge } from 'k6/metrics';

// ============================================================================
// Configuration
// ============================================================================

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const DURATION = __ENV.DURATION || '5m';

// Custom metrics
const cacheHitRate = new Rate('cache_hit_rate');
const dbQueryDuration = new Trend('db_query_duration');
const cacheLatency = new Trend('cache_latency');
const activeUsers = new Gauge('active_users');
const errorsByType = new Counter('errors_by_type');

// ============================================================================
// Test Configuration with Scenarios
// ============================================================================

export const options = {
  scenarios: {
    // Scenario 1: Constant background load
    background_load: {
      executor: 'constant-vus',
      vus: 10,
      duration: DURATION,
      exec: 'backgroundLoad',
      tags: { scenario: 'background' },
    },
    
    // Scenario 2: Spike test - sudden traffic increase
    spike_test: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '10s', target: 0 },   // Wait 10s
        { duration: '30s', target: 50 },  // Spike to 50 users
        { duration: '1m', target: 50 },   // Hold 50 users
        { duration: '30s', target: 0 },   // Drop to 0
      ],
      startTime: '1m',
      exec: 'spikeTest',
      tags: { scenario: 'spike' },
    },
    
    // Scenario 3: Stress test - gradually increase load
    stress_test: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '2m', target: 20 },
        { duration: '2m', target: 50 },
        { duration: '2m', target: 100 },
        { duration: '1m', target: 150 },
        { duration: '1m', target: 0 },
      ],
      startTime: '2m',
      exec: 'stressTest',
      tags: { scenario: 'stress' },
    },
    
    // Scenario 4: Soak test - sustained load
    soak_test: {
      executor: 'constant-vus',
      vus: 30,
      duration: '3m',
      startTime: '30s',
      exec: 'soakTest',
      tags: { scenario: 'soak' },
    },
    
    // Scenario 5: API health monitoring
    health_monitor: {
      executor: 'constant-arrival-rate',
      rate: 1,
      timeUnit: '10s',
      duration: DURATION,
      preAllocatedVUs: 1,
      exec: 'healthMonitor',
      tags: { scenario: 'monitoring' },
    },
  },
  
  thresholds: {
    // HTTP thresholds
    'http_req_duration': ['p(95)<500', 'p(99)<1000'],
    'http_req_duration{scenario:background}': ['p(95)<300'],
    'http_req_duration{scenario:spike}': ['p(95)<1000'],
    'http_req_failed': ['rate<0.05'],
    
    // Custom thresholds
    'cache_hit_rate': ['rate>0.50'],
    'db_query_duration': ['p(95)<100'],
    'cache_latency': ['p(95)<20'],
    'errors_by_type': ['count<100'],
  },
};

// ============================================================================
// Scenario 1: Background Load - Normal operations
// ============================================================================

export function backgroundLoad() {
  const scenarios = [
    () => testUserOperations(),
    () => testCacheOperations(),
    () => testDatabaseReads(),
  ];
  
  const scenario = scenarios[Math.floor(Math.random() * scenarios.length)];
  scenario();
  
  sleep(Math.random() * 3 + 1); // 1-4 seconds
}

// ============================================================================
// Scenario 2: Spike Test - Handle sudden traffic
// ============================================================================

export function spikeTest() {
  group('Spike: User Flow', function() {
    // Simulate user accessing multiple endpoints rapidly
    
    // 1. List users
    let res = http.get(`${BASE_URL}/api/users`);
    check(res, { 'spike: list users ok': (r) => r.status === 200 });
    
    // 2. Get random users
    for (let i = 0; i < 3; i++) {
      const userId = Math.floor(Math.random() * 100) + 1;
      res = http.get(`${BASE_URL}/api/users/${userId}`);
      check(res, { 'spike: get user ok': (r) => r.status === 200 });
      sleep(0.1);
    }
    
    // 3. Cache operations
    const cacheKey = `spike_${Date.now()}_${Math.random()}`;
    res = http.get(`${BASE_URL}/api/cache/${cacheKey}`);
    
    if (res.status === 404) {
      const payload = JSON.stringify({ value: 'test_value' });
      http.post(`${BASE_URL}/api/cache/${cacheKey}`, payload, {
        headers: { 'Content-Type': 'application/json' },
      });
    }
  });
  
  sleep(0.5);
}

// ============================================================================
// Scenario 3: Stress Test - Push limits
// ============================================================================

export function stressTest() {
  activeUsers.add(1);
  
  group('Stress: Heavy Operations', function() {
    const operations = Math.floor(Math.random() * 5) + 3; // 3-7 operations
    
    for (let i = 0; i < operations; i++) {
      const rand = Math.random();
      
      if (rand < 0.4) {
        // Database writes (more intensive)
        const payload = JSON.stringify({
          username: `stress_user_${Date.now()}_${i}`,
        });
        http.post(`${BASE_URL}/api/users`, payload, {
          headers: { 'Content-Type': 'application/json' },
        });
      } else if (rand < 0.7) {
        // Cache operations
        const key = `stress_${Math.floor(Math.random() * 50)}`;
        http.get(`${BASE_URL}/api/cache/${key}`);
      } else {
        // Database reads
        const userId = Math.floor(Math.random() * 1000) + 1;
        http.get(`${BASE_URL}/api/users/${userId}`);
      }
      
      sleep(0.1);
    }
  });
  
  activeUsers.add(-1);
  sleep(Math.random() * 2);
}

// ============================================================================
// Scenario 4: Soak Test - Sustained load
// ============================================================================

export function soakTest() {
  group('Soak: Continuous Operations', function() {
    // Mix of all operations to simulate real usage over time
    
    // Read operations (70%)
    if (Math.random() < 0.7) {
      http.get(`${BASE_URL}/api/users`);
      
      const userId = Math.floor(Math.random() * 100) + 1;
      http.get(`${BASE_URL}/api/users/${userId}`);
      
      const cacheKey = `user_${userId}`;
      const start = Date.now();
      const res = http.get(`${BASE_URL}/api/cache/${cacheKey}`);
      cacheLatency.add(Date.now() - start);
      cacheHitRate.add(res.status === 200 && res.json('hit') === true);
    } 
    // Write operations (20%)
    else if (Math.random() < 0.9) {
      const payload = JSON.stringify({
        username: `soak_user_${Date.now()}`,
      });
      
      const start = Date.now();
      http.post(`${BASE_URL}/api/users`, payload, {
        headers: { 'Content-Type': 'application/json' },
      });
      dbQueryDuration.add(Date.now() - start);
    }
    // Random/error operations (10%)
    else {
      const res = http.get(`${BASE_URL}/api/random`);
      if (res.status !== 200) {
        errorsByType.add(1, { error_type: 'random_error' });
      }
    }
  });
  
  sleep(Math.random() * 2 + 0.5);
}

// ============================================================================
// Scenario 5: Health Monitor
// ============================================================================

export function healthMonitor() {
  group('Monitoring', function() {
    // Health check
    let res = http.get(`${BASE_URL}/health`);
    check(res, {
      'health: status 200': (r) => r.status === 200,
      'health: fast response': (r) => r.timings.duration < 100,
    });
    
    // Readiness check
    res = http.get(`${BASE_URL}/ready`);
    check(res, {
      'ready: status 200': (r) => r.status === 200,
      'ready: observability enabled': (r) => r.json('observability') === true,
    });
    
    // Metrics info
    res = http.get(`${BASE_URL}/metrics/info`);
    check(res, {
      'metrics: status 200': (r) => r.status === 200,
      'metrics: initialized': (r) => r.json('observability.initialized') === true,
    });
  });
}

// ============================================================================
// Helper Functions
// ============================================================================

function testUserOperations() {
  group('User Operations', function() {
    const operations = ['list', 'get', 'create', 'update', 'delete'];
    const operation = operations[Math.floor(Math.random() * operations.length)];
    
    const start = Date.now();
    let res;
    
    switch (operation) {
      case 'list':
        res = http.get(`${BASE_URL}/api/users`);
        break;
      case 'get':
        const userId = Math.floor(Math.random() * 100) + 1;
        res = http.get(`${BASE_URL}/api/users/${userId}`);
        break;
      case 'create':
        res = http.post(`${BASE_URL}/api/users`, JSON.stringify({
          username: `user_${Date.now()}`,
        }), { headers: { 'Content-Type': 'application/json' } });
        break;
      case 'update':
        const updateId = Math.floor(Math.random() * 100) + 1;
        res = http.put(`${BASE_URL}/api/users/${updateId}`, JSON.stringify({
          username: `updated_${Date.now()}`,
        }), { headers: { 'Content-Type': 'application/json' } });
        break;
      case 'delete':
        const deleteId = Math.floor(Math.random() * 100) + 1;
        res = http.del(`${BASE_URL}/api/users/${deleteId}`);
        break;
    }
    
    dbQueryDuration.add(Date.now() - start);
    
    check(res, {
      [`${operation}: success`]: (r) => r.status >= 200 && r.status < 300,
    });
    
    if (res.status >= 400) {
      errorsByType.add(1, { error_type: `db_${operation}_error` });
    }
  });
}

function testCacheOperations() {
  group('Cache Operations', function() {
    const cacheKey = `key_${Math.floor(Math.random() * 50)}`;
    const start = Date.now();
    
    const res = http.get(`${BASE_URL}/api/cache/${cacheKey}`);
    const duration = Date.now() - start;
    
    cacheLatency.add(duration);
    cacheHitRate.add(res.status === 200 && res.json('hit') === true);
    
    if (res.status === 404) {
      const setStart = Date.now();
      const setRes = http.post(`${BASE_URL}/api/cache/${cacheKey}`, 
        JSON.stringify({ value: `value_${Date.now()}` }),
        { headers: { 'Content-Type': 'application/json' } }
      );
      cacheLatency.add(Date.now() - setStart);
      
      if (setRes.status !== 200) {
        errorsByType.add(1, { error_type: 'cache_set_error' });
      }
    }
  });
}

function testDatabaseReads() {
  group('Database Reads', function() {
    const start = Date.now();
    
    // List operation
    let res = http.get(`${BASE_URL}/api/users`);
    
    if (res.status === 200) {
      // Get specific user
      const userId = Math.floor(Math.random() * 100) + 1;
      res = http.get(`${BASE_URL}/api/users/${userId}`);
    }
    
    dbQueryDuration.add(Date.now() - start);
    
    if (res.status !== 200) {
      errorsByType.add(1, { error_type: 'db_read_error' });
    }
  });
}

// ============================================================================
// Setup & Teardown
// ============================================================================

export function setup() {
  console.log('Starting advanced load test');
  console.log(`Target: ${BASE_URL}`);
  console.log(`Duration: ${DURATION}`);
  console.log('');
  
  const res = http.get(`${BASE_URL}/health`);
  if (res.status !== 200) {
    throw new Error('Service not available');
  }
  
  return { startTime: Date.now() };
}

export function teardown(data) {
  const duration = (Date.now() - data.startTime) / 1000;
  
  console.log('');
  console.log('Test completed');
  console.log(`Total time: ${duration.toFixed(2)}s`);
  console.log('');
  console.log('View results in:');
  console.log('   - K6 summary above');
  console.log('   - Grafana dashboard');
  console.log('');
}