import { NextResponse } from 'next/server'
import type { NextRequest } from 'next/server'

export function proxy(request: NextRequest) {
  // Add a custom header to the response to verify Edge execution
  const response = NextResponse.next()
  
  if (request.nextUrl.pathname.startsWith('/proxy-test')) {
    response.headers.set('x-proxy-custom-header', 'hello-from-edge-proxy')
    // Rewrite to a secret page just to show proxy works over CloudFront correctly
    if (request.nextUrl.searchParams.has('rewrite')) {
      return NextResponse.rewrite(new URL('/rewrite-target', request.url))
    }
  }

  return response
}

export const config = {
  matcher: ['/proxy-test/:path*'],
}
