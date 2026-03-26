import { NextResponse } from 'next/server';

export async function GET() {
  // Test reading from the injected environment map at runtime
  const testSecret = process.env.TEST_SECRET_KEY || 'NOT_FOUND_OR_NOT_INJECTED';
  
  return NextResponse.json({
    secretValue: testSecret,
    environment: process.env.NODE_ENV,
    timestamp: new Date().toISOString(),
  });
}
