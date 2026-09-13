"""Real Go backend protocol requests from the chat page; only upstream is synthetic."""
import json
import os
from urllib.request import Request,urlopen
from playwright.sync_api import sync_playwright,expect

origin='http://127.0.0.1:18317'
headers={'Authorization':'Bearer '+os.environ['COT_ADMIN_KEY'],'Content-Type':'application/json'}
def admin(path,body=None):
    with urlopen(Request(origin+path,headers=headers,data=None if body is None else json.dumps(body).encode(),method='GET' if body is None else 'PATCH')) as r:
        return json.load(r)
current=admin('/admin/config')
c=current['config']
for protocol,adapter in [('chat','openai'),('responses','openai'),('messages','anthropic'),('gemini','gemini')]:
    c['sources'].append({'id':'protocol-'+protocol,'provider':'protocol-fixture','adapter':adapter,'base_url':os.environ['COT_UI_UPSTREAM'],'local':True,'enabled':True,'max_inflight':1,'quota_domain':'protocol-'+protocol,'quota_max_inflight':1,'models':[{'id':'m','upstream':'m','protocols':[protocol],'tier':'unrated','tools':'none','max_input_bytes':4096}]})
admin('/admin/config',{'revision':current['revision'],'config':c,'summary':'Isolated protocol fixture'})
with sync_playwright() as p:
    browser=p.chromium.launch(headless=True)
    page=browser.new_page()
    errors=[]
    page.on('pageerror',lambda e:errors.append(str(e)))
    page.goto(origin+'/chat')
    page.locator('#key').fill(os.environ['COT_API_KEY'])
    page.locator('#connect').click()
    expect(page.locator('#status')).to_have_text('已连接')
    for protocol in ['chat','responses','messages','gemini']:
        page.locator('#new').click()
        page.locator('#model').select_option('protocol-'+protocol+'/m')
        expect(page.locator('#protocol option')).to_have_count(1)
        expect(page.locator('#protocol')).to_have_value(protocol)
        page.locator('#prompt').fill('你好\nSecond line')
        page.locator('#send').click()
        expect(page.locator('#status')).to_contain_text('已完成',timeout=10000)
        expect(page.locator('.assistant .content')).to_have_text('OK')
    assert not errors,errors
    # Publish enough disabled sources to require pagination. These never appear
    # as callable models and must not cause upstream requests by being listed.
    current=admin('/admin/config');c=current['config']
    for i in range(110):
        value=json.loads(json.dumps(c['sources'][-1]));value.update(id='paged-'+str(i),enabled=False,quota_domain='paged-'+str(i));c['sources'].append(value)
    admin('/admin/config',{'revision':current['revision'],'config':c,'summary':'Disabled pagination fixtures'})
    page.goto(origin)
    page.get_by_label('Admin key',exact=True).fill(os.environ['COT_ADMIN_KEY'])
    page.get_by_role('button',name='Connect',exact=True).last.click()
    page.get_by_role('link',name='Sources',exact=True).click()
    expect(page.get_by_role('button',name='Next page',exact=True)).to_be_enabled()
    assert page.get_by_role('row').count()<=101
    page.get_by_role('button',name='Next page',exact=True).click()
    page.get_by_label('Filter table rows',exact=True).fill('paged-109')
    expect(page.get_by_role('button',name='paged-109',exact=True)).to_be_visible()
    assert page.get_by_role('row').count()==2
    browser.close()
print('Chat UI protocols and large-table pagination passed through the actual Go backend.')
