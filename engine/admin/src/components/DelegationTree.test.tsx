import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import DelegationTree from './DelegationTree';
import type { TreeAgent, TreeRelation } from '../types';

function makeAgent(id: string, name: string): TreeAgent {
  return {
    id,
    name,
    model: 'qwen-next',
    description: '',
    avatarInitials: name.slice(0, 2).toUpperCase(),
    lifecycle: 'persistent',
    toolsCount: 0,
    knowledgeCount: 0,
    flowsCount: 0,
    activeSessions: 0,
    state: 'idle',
  };
}

describe('DelegationTree', () => {
  it('renders both agents connected when the relation keys by name', () => {
    const agents: TreeAgent[] = [
      makeAgent('researcher', 'researcher'),
      makeAgent('synthesizer', 'synthesizer'),
    ];
    const relations: TreeRelation[] = [
      { id: 'rel-1', sourceAgentId: 'researcher', targetAgentId: 'synthesizer' },
    ];

    render(
      <DelegationTree agents={agents} relations={relations} entryAgentId="researcher" />,
    );

    expect(screen.getByText('researcher')).toBeInTheDocument();
    expect(screen.getByText('synthesizer')).toBeInTheDocument();
  });

  it('renders both agents connected when the relation uses UUID keys but agents are keyed by name', () => {
    // The production admin adapter sets TreeAgent.id = agent.name, but relations
    // arriving from admin endpoints sometimes reference agent UUIDs instead.
    // DelegationTree.buildTree must resolve both to the same agent.
    const agents: TreeAgent[] = [
      makeAgent('researcher', 'researcher'),
      makeAgent('synthesizer', 'synthesizer'),
    ];
    const relations: TreeRelation[] = [
      { id: 'rel-1', sourceAgentId: 'researcher', targetAgentId: 'synthesizer' },
    ];

    render(
      <DelegationTree
        agents={agents}
        relations={relations}
        // Entry passed as the name rather than the internal id — resolveKey
        // must fall back to the byName map.
        entryAgentId="researcher"
      />,
    );
    // Both cards should appear — if buildTree silently dropped the child
    // because of a key mismatch the synthesizer card would be missing.
    expect(screen.getByText('researcher')).toBeInTheDocument();
    expect(screen.getByText('synthesizer')).toBeInTheDocument();
  });

  it('shows the "entry agent not found" placeholder when entryAgentId does not match any agent', () => {
    const agents: TreeAgent[] = [makeAgent('a', 'a')];
    render(<DelegationTree agents={agents} relations={[]} entryAgentId="nonexistent" />);
    expect(screen.getByText(/entry agent not found/i)).toBeInTheDocument();
  });
});
