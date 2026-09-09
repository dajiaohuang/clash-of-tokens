"""Explicit live smoke test: sends two short prompts to the signed-in account."""
import argparse
import json
import pathlib
import subprocess
import time
import urllib.request
import urllib.error

parser = argparse.ArgumentParser()
parser.add_argument('--live', action='store_true', required=True)
parser.add_argument('--stream', action='store_true')
parser.add_argument('--previous-id', help='Resume an existing smoke response after restarting the gateway')
args = parser.parse_args()
root = pathlib.Path(__file__).resolve().parents[1]
cfg = json.loads((root / 'config.json').read_text())
keys = json.loads(subprocess.check_output([str(root / 'dist/clash-tokens.exe'), 'keys'], cwd=root))
url = 'http://' + cfg['listen'] + '/v1/responses'
previous = args.previous_id
prompts = ['What was the codeword in my previous message? Reply with only the codeword.']
if not previous:
    prompts.insert(0, 'Remember the codeword amber-sparrow-47. Reply with only that codeword.')
for prompt in prompts:
    body = {'model': 'chatgpt-web/web', 'input': prompt, 'stream': args.stream}
    if previous:
        body['previous_response_id'] = previous
    request = urllib.request.Request(url, json.dumps(body).encode(), headers={'Authorization': 'Bearer ' + keys['api_key'], 'Content-Type': 'application/json'})
    start = time.monotonic()
    try:
        with urllib.request.urlopen(request, timeout=180) as response:
            if args.stream:
                result = None
                deltas = []
                for raw in response:
                    if not raw.startswith(b'data: '):
                        continue
                    data = json.loads(raw[6:])
                    if data.get('type') == 'response.output_text.delta':
                        deltas.append(data['delta'])
                    if data.get('type') == 'response.completed':
                        result = data['response']
                assert result is not None, 'stream missing response.completed'
                assert ''.join(deltas) == 'amber-sparrow-47', 'stream delta content mismatch'
            else:
                result = json.load(response)
            previous = result.get('id')
            assert previous and result.get('status') == 'completed', 'response did not complete'
            text = ''.join(part.get('text', '') for item in result.get('output', []) for part in item.get('content', []))
            assert text == 'amber-sparrow-47', 'live response/history mismatch'
            print(json.dumps({'seconds': round(time.monotonic()-start, 3), 'response': result}, ensure_ascii=False), flush=True)
    except urllib.error.HTTPError as error:
        print(json.dumps({'status': error.code, 'body': error.read().decode()}), flush=True)
        raise SystemExit(1)
