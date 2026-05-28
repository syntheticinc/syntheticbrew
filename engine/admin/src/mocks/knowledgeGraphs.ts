// Mock data for Knowledge Graphs page (prototype mode).
//
// Backend API is not deployed yet — these mocks let the UI render and be
// exercised in prototype mode. Real API calls will replace them once the
// engine exposes /api/v1/knowledge-graphs.
//
// Naming is intentionally generic ("industrial-iot-taxonomy") to avoid
// client/partner names. Schemas (industry, sensor_family, use_case) are
// representative of a small taxonomy bundle.

import type { KGBundle, KGEntitySchema, KGEntity } from '../types';

// ─── Bundles ────────────────────────────────────────────────────────────────

export const MOCK_KG_BUNDLES: KGBundle[] = [
  {
    bundle_name: 'industrial-iot-taxonomy',
    version: '0.3.1',
    manifest: {
      entity_types: ['industry', 'sensor_family', 'use_case'],
      counts: { industry: 8, sensor_family: 10, use_case: 9 },
      schema_hashes: {
        industry: 'sha256:c4d1a2b9',
        sensor_family: 'sha256:8b22f0e7',
        use_case: 'sha256:71e9adcb',
      },
    },
    created_at: '2026-05-01T09:30:00Z',
    updated_at: '2026-05-22T14:12:00Z',
  },
];

// ─── Entity Schemas ─────────────────────────────────────────────────────────

export const MOCK_KG_SCHEMAS: Record<string, KGEntitySchema[]> = {
  'industrial-iot-taxonomy': [
    {
      bundle_name: 'industrial-iot-taxonomy',
      entity_type: 'industry',
      id_field: 'id',
      schema_hash: 'sha256:c4d1a2b9',
      expose_tools: ['lookup_industry', 'list_industries'],
      tool_description: 'Vertical industry segments for IoT deployments',
      schema_json: {
        type: 'object',
        required: ['id', 'name'],
        properties: {
          id: { type: 'string', 'x-index': true },
          name: { type: 'string', 'x-index': true },
          parent_industry_id: { type: 'string', 'x-cross-ref': 'industry' },
          description: { type: 'string' },
        },
      },
    },
    {
      bundle_name: 'industrial-iot-taxonomy',
      entity_type: 'sensor_family',
      id_field: 'id',
      schema_hash: 'sha256:8b22f0e7',
      expose_tools: ['lookup_sensor_family'],
      tool_description: 'Families of physical sensors used in monitoring',
      schema_json: {
        type: 'object',
        required: ['id', 'name', 'measurement'],
        properties: {
          id: { type: 'string', 'x-index': true },
          name: { type: 'string', 'x-index': true },
          measurement: { type: 'string', 'x-index': true, enum: ['temperature', 'pressure', 'vibration', 'humidity', 'flow', 'level'] },
          unit: { type: 'string' },
          typical_range: { type: 'string' },
        },
      },
    },
    {
      bundle_name: 'industrial-iot-taxonomy',
      entity_type: 'use_case',
      id_field: 'id',
      schema_hash: 'sha256:71e9adcb',
      expose_tools: ['lookup_use_case', 'list_use_cases'],
      tool_description: 'Recurring operational scenarios where sensors are deployed',
      schema_json: {
        type: 'object',
        required: ['id', 'name'],
        properties: {
          id: { type: 'string', 'x-index': true },
          name: { type: 'string', 'x-index': true },
          industry_id: { type: 'string', 'x-cross-ref': 'industry', 'x-index': true },
          sensor_family_ids: { type: 'array', items: { type: 'string', 'x-cross-ref': 'sensor_family' } },
          summary: { type: 'string' },
        },
      },
    },
  ],
};

// ─── Entities ───────────────────────────────────────────────────────────────

const industries: Array<[string, string]> = [
  ['manufacturing', 'Manufacturing'],
  ['energy', 'Energy & Utilities'],
  ['oil_gas', 'Oil & Gas'],
  ['agriculture', 'Agriculture'],
  ['logistics', 'Logistics & Transport'],
  ['mining', 'Mining'],
  ['water', 'Water & Wastewater'],
  ['buildings', 'Smart Buildings'],
];

const sensorFamilies: Array<[string, string, string, string]> = [
  ['temp', 'Temperature Sensor', 'temperature', '°C'],
  ['pressure', 'Pressure Transducer', 'pressure', 'bar'],
  ['vibration', 'Vibration Probe', 'vibration', 'mm/s'],
  ['humidity', 'Humidity Sensor', 'humidity', '%RH'],
  ['flow_mag', 'Magnetic Flow Meter', 'flow', 'L/min'],
  ['flow_ultra', 'Ultrasonic Flow Meter', 'flow', 'L/min'],
  ['level_radar', 'Radar Level Sensor', 'level', 'm'],
  ['level_capacitive', 'Capacitive Level Sensor', 'level', 'm'],
  ['ph', 'pH Sensor', 'level', 'pH'],
  ['gas_co2', 'CO2 Sensor', 'level', 'ppm'],
];

const useCases: Array<[string, string, string, string[]]> = [
  ['predictive_maintenance', 'Predictive Maintenance', 'manufacturing', ['vibration', 'temp']],
  ['cold_chain', 'Cold Chain Monitoring', 'logistics', ['temp', 'humidity']],
  ['leak_detection', 'Pipeline Leak Detection', 'oil_gas', ['pressure', 'flow_ultra']],
  ['greenhouse', 'Greenhouse Climate Control', 'agriculture', ['humidity', 'temp', 'gas_co2']],
  ['energy_meter', 'Energy Sub-metering', 'energy', ['flow_mag']],
  ['tank_level', 'Storage Tank Level', 'water', ['level_radar', 'level_capacitive']],
  ['water_quality', 'Drinking Water Quality', 'water', ['ph', 'temp']],
  ['mine_air', 'Underground Air Quality', 'mining', ['gas_co2', 'humidity']],
  ['hvac_balancing', 'HVAC Balancing', 'buildings', ['temp', 'pressure', 'humidity']],
];

function entitiesForBundle(): Record<string, KGEntity[]> {
  const bundle = 'industrial-iot-taxonomy';
  return {
    industry: industries.map(([id, name]) => ({
      bundle_name: bundle,
      entity_type: 'industry',
      entity_id: id,
      schema_hash: 'sha256:c4d1a2b9',
      data: { id, name, description: `${name} sector` },
    })),
    sensor_family: sensorFamilies.map(([id, name, measurement, unit]) => ({
      bundle_name: bundle,
      entity_type: 'sensor_family',
      entity_id: id,
      schema_hash: 'sha256:8b22f0e7',
      data: { id, name, measurement, unit, typical_range: '' },
    })),
    use_case: useCases.map(([id, name, industry_id, sensors]) => ({
      bundle_name: bundle,
      entity_type: 'use_case',
      entity_id: id as string,
      schema_hash: 'sha256:71e9adcb',
      data: {
        id,
        name,
        industry_id,
        sensor_family_ids: sensors,
        summary: `Common ${name.toString().toLowerCase()} scenario`,
      },
    })),
  };
}

export const MOCK_KG_ENTITIES: Record<string, Record<string, KGEntity[]>> = {
  'industrial-iot-taxonomy': entitiesForBundle(),
};
