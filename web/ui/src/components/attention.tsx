import { useState } from 'react';

import { useResolveUserInput, useResolveValidation } from '@/api/queries';
import type { UserInputRequest, ValidationRequest } from '@/api/types';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Input } from '@/components/ui/input';

/**
 * What is waiting for the user, right where the work is.
 *
 * Pending attention is current state, not a count of unread events: a request
 * answered on another device disappears here the moment the stream says so
 * (spec section 6).
 */
export function AttentionPanel({
  validations,
  userInputs,
}: {
  validations: ValidationRequest[];
  userInputs: UserInputRequest[];
}) {
  if (validations.length === 0 && userInputs.length === 0) return null;

  return (
    <div className="space-y-2">
      {validations.map((request) => (
        <ValidationCard key={request.id} request={request} />
      ))}
      {userInputs.map((request) => (
        <UserInputCard key={request.id} request={request} />
      ))}
    </div>
  );
}

function ValidationCard({ request }: { request: ValidationRequest }) {
  const resolve = useResolveValidation();

  return (
    <Card className="border-warn/40 bg-warn/5">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium">{request.title}</span>
        {request.originChannel && <Badge>{request.originChannel}</Badge>}
      </div>
      {request.summary && <p className="text-muted mt-1 text-sm">{request.summary}</p>}

      <pre className="bg-surface-2 text-muted mt-2 max-h-40 overflow-auto rounded p-2 font-mono text-xs">
        {JSON.stringify(request.requestPayload, null, 2)}
      </pre>

      <div className="mt-3 flex gap-2">
        <Button
          variant="primary"
          size="sm"
          disabled={resolve.isPending}
          onClick={() => resolve.mutate({ id: request.id, approved: true })}
        >
          Approve
        </Button>
        <Button
          variant="secondary"
          size="sm"
          disabled={resolve.isPending}
          onClick={() => resolve.mutate({ id: request.id, approved: false })}
        >
          Deny
        </Button>
      </div>
    </Card>
  );
}

function UserInputCard({ request }: { request: UserInputRequest }) {
  const resolve = useResolveUserInput();
  const [value, setValue] = useState('');
  const choices = request.choices ?? [];

  return (
    <Card className="border-accent/40 bg-accent/5">
      <p className="text-sm whitespace-pre-wrap">{request.prompt}</p>

      <div className="mt-3 flex flex-wrap gap-2">
        {choices.map((choice) => (
          <Button
            key={choice}
            size="sm"
            disabled={resolve.isPending}
            onClick={() => resolve.mutate({ id: request.id, value: choice })}
          >
            {choice}
          </Button>
        ))}
      </div>

      {(request.freeText || choices.length === 0) && (
        <form
          className="mt-2 flex gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            const answer = value.trim();
            if (answer) resolve.mutate({ id: request.id, value: answer });
          }}
        >
          <Input
            value={value}
            onChange={(event) => setValue(event.target.value)}
            placeholder="Your answer"
            autoFocus
          />
          <Button type="submit" variant="primary" size="sm" disabled={resolve.isPending}>
            Answer
          </Button>
        </form>
      )}
    </Card>
  );
}
