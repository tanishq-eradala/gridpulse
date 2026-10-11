"""Exercise telemetry ingestion, streaming, alerts and polling recovery."""
import argparse
import json
from datetime import datetime, timezone
from pathlib import Path

def main():
    from playwright.sync_api import sync_playwright, expect
    parser=argparse.ArgumentParser();parser.add_argument('--api-url',default='http://127.0.0.1:8096');parser.add_argument('--base-url',default='http://127.0.0.1:5186');parser.add_argument('--channel');parser.add_argument('--output',type=Path,default=Path(__file__).resolve().parents[1]/'docs/screenshots');args=parser.parse_args()
    args.output.mkdir(parents=True,exist_ok=True);checks=[]
    def passed(text):checks.append(text);print('PASS',text,flush=True)
    with sync_playwright() as p:
        browser=p.chromium.launch(headless=True,**({'channel':args.channel} if args.channel else {}))
        context=browser.new_context(viewport={'width':1440,'height':1000});page=context.new_page();errors=[]
        page.on('pageerror',lambda e:errors.append(str(e)))
        assert context.request.get(args.api_url+'/api/summary').json()['totalReadings']==0,'Use a disposable empty database.'
        page.goto(args.base_url);page.wait_for_load_state('domcontentloaded')
        expect(page.get_by_role('heading',name='Equipment telemetry',exact=True)).to_be_visible()
        expect(page.locator('.connection')).to_have_text('Live stream');expect(page.locator('.chart-empty')).to_contain_text('Waiting')
        passed('empty workspace and live SSE connection')
        seq=0
        def reading(temp,unit='lab-unit-01'):
            nonlocal seq
            seq+=1
            return {'sampleId':f'browser-sample-{seq:04d}','equipmentId':unit,'recordedAt':datetime.now(timezone.utc).isoformat(timespec='milliseconds').replace('+00:00','Z'),'voltage':480,'current':120,'temperature':temp,'efficiency':96.5}
        def post(payload,status=200):
            r=context.request.post(args.api_url+'/api/readings',data=payload);assert r.status==status,(r.status,r.text());return r.json()
        normal=reading(65);post(normal)
        expect(page.get_by_text('1 readings ingested',exact=True)).to_be_visible();expect(page.locator('.health-ring')).to_have_text('Normal')
        passed('SSE updates the dashboard after a valid sample')
        post(reading(90));post(reading(91))
        assert context.request.get(args.api_url+'/api/summary').json()['activeAlerts']==0
        passed('two abnormal samples do not open a persistent alert')
        hot=reading(92);post(hot)
        expect(page.locator('.badge')).to_have_text('1 active');expect(page.locator('.health-ring')).to_have_text('Attention')
        alerts=context.request.get(args.api_url+'/api/alerts').json();assert len(alerts)==1 and alerts[0]['active']
        expect(page.get_by_role('img',name='temperature trend in °C')).to_be_visible()
        passed('third abnormal sample opens one durable alert and chart')
        page.screenshot(path=str(args.output/'active-alert-desktop.png'),full_page=True)
        replay=post(hot);assert not replay['inserted'];assert context.request.get(args.api_url+'/api/summary').json()['totalReadings']==4
        passed('identical sample retry leaves counts and alert history unchanged')
        post({**hot,'temperature':93},409);assert context.request.get(args.api_url+'/api/summary').json()['totalReadings']==4
        passed('changed payload with a reused sample ID is rejected')
        post(reading(300),400);post(reading(65,'missing-unit'),404)
        assert context.request.get(args.api_url+'/api/summary').json()['totalReadings']==4
        passed('invalid readings and unknown equipment do not persist')
        post(reading(64))
        expect(page.locator('.badge')).to_have_text('0 active');expect(page.locator('.alert-row em')).to_have_text('Resolved')
        alerts=context.request.get(args.api_url+'/api/alerts').json();assert len(alerts)==1 and not alerts[0]['active']
        passed('normal sample resolves the alert without erasing history')
        page.get_by_role('combobox').select_option('lab-unit-02')
        expect(page.locator('.health-ring')).to_have_text('Waiting');expect(page.locator('.chart-empty')).to_be_visible()
        page.get_by_role('combobox').select_option('lab-unit-01');expect(page.locator('.health-ring')).to_have_text('Normal')
        passed('equipment selection isolates readings and equipment state')
        page.route('**/api/stream',lambda route:route.abort())
        page.reload();expect(page.locator('.connection')).to_have_text('Polling fallback')
        post(reading(67));expect(page.get_by_text('6 readings ingested',exact=True)).to_be_visible(timeout=15000)
        passed('polling recovers new readings while SSE is unavailable')
        page.unroute('**/api/stream');page.reload();expect(page.locator('.connection')).to_have_text('Live stream')
        passed('live stream reconnects after recovery')
        metrics=context.request.get(args.api_url+'/metrics').text();assert 'gridpulse_readings_total 6' in metrics and 'gridpulse_active_alerts 0' in metrics
        passed('Prometheus metrics match persisted readings and alert status')
        page.screenshot(path=str(args.output/'resolved-dashboard.png'),full_page=True)
        for width in (390,320):
            page.set_viewport_size({'width':width,'height':844})
            assert page.evaluate('document.documentElement.scrollWidth <= innerWidth'),f'Overflow at {width}px'
            expect(page.get_by_role('combobox')).to_be_visible()
            passed(f'{width}px layout containment')
        page.screenshot(path=str(args.output/'telemetry-mobile.png'),full_page=True)
        assert not errors,errors;passed('no unhandled browser errors')
        (args.output/'verification.json').write_text(json.dumps({'passed':len(checks),'checks':checks,'scope':'Synthetic readings in a disposable database. No physical equipment or sustained-load validation.'},indent=2))
        browser.close();print(f'{len(checks)} workflow checks passed.')

if __name__=='__main__':main()
