import type { components } from './generated-types';
const response: components['schemas']['Probe'] = { status: 'ready' };
// @ts-expect-error This probe has only the ready state.
const invalid: components['schemas']['Probe'] = { status: 'invalid' };
void [response, invalid];
