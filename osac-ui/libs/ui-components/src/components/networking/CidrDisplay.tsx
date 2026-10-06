import React from 'react';

interface CidrDisplayProps {
  ipv4Cidr?: string;
}

export const CidrDisplay: React.FC<CidrDisplayProps> = ({ ipv4Cidr }) => <>{ipv4Cidr || '—'}</>;
