import type { Monitor } from '@/api/types';
import { IDS, at } from './data';
export const monitors: Monitor[] = [{
 id:'d0000099-0000-4000-8000-000000000001', sessionId:IDS.grafana,backendId:IDS.nas,
 runId:'d0000099-0000-4000-8000-000000000002',jobId:'d0000099-0000-4000-8000-000000000003',
 description:'Watch deployment checks',command:'deployment-checks --watch',state:'RUNNING',sequence:2,
 output:'Health check passed. Waiting for the next change.',startedAt:at(8),updatedAt:at(1),expiresAt:at(-52),
 notify:false,automaticResumesRemaining:19,confirmed:true,lastSeenAt:at(0),
}];
