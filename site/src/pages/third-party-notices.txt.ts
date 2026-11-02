import notices from '../../../internal/notices/THIRD_PARTY_NOTICES.txt?raw';

export function GET() {
  return new Response(notices, {
    headers: { 'Content-Type': 'text/plain; charset=utf-8' },
  });
}
