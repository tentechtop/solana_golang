package network.ordexus.node;

import android.Manifest;
import android.app.Activity;
import android.content.*;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.*;
import android.provider.MediaStore;
import android.util.Base64;
import android.webkit.*;
import android.widget.*;
import java.io.*;
import java.util.concurrent.*;

public final class MainActivity extends Activity {
    private WebView panel;
    private String origin;
    private boolean bound;
    private byte[] pendingPNG;
    private final ExecutorService files=Executors.newSingleThreadExecutor();
    private final Messenger replies=new Messenger(new Handler(Looper.getMainLooper()){
        @Override public void handleMessage(Message message){
            if(message.what!=NodeService.PANEL)return;
            String address=message.getData().getString("url");
            if(address==null){Toast.makeText(MainActivity.this,message.getData().getString("error","节点内核无法加载"),Toast.LENGTH_LONG).show();return;}
            Uri uri=Uri.parse(address);origin=uri.getScheme()+"://"+uri.getAuthority();panel.loadUrl(address);
        }
    });
    private final ServiceConnection connection=new ServiceConnection(){
        @Override public void onServiceConnected(ComponentName name,IBinder binder){bound=true;Message request=Message.obtain(null,NodeService.PANEL);request.replyTo=replies;try{new Messenger(binder).send(request);}catch(RemoteException error){Toast.makeText(MainActivity.this,"节点服务连接失败",Toast.LENGTH_LONG).show();}}
        @Override public void onServiceDisconnected(ComponentName name){Toast.makeText(MainActivity.this,"节点服务已退出，请重新打开 APP",Toast.LENGTH_LONG).show();}
    };
    @Override public void onCreate(Bundle state){
        super.onCreate(state);
        panel=new WebView(this);panel.setBackgroundColor(0xfff2f6f7);setContentView(panel);
        panel.getSettings().setJavaScriptEnabled(true);panel.getSettings().setDomStorageEnabled(false);
        panel.getSettings().setAllowFileAccess(false);panel.getSettings().setAllowContentAccess(false);
        panel.getSettings().setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
        panel.setWebViewClient(new WebViewClient(){@Override public boolean shouldOverrideUrlLoading(WebView view,WebResourceRequest request){return origin==null || !(origin.equals(request.getUrl().getScheme()+"://"+request.getUrl().getAuthority()));}});
        panel.addJavascriptInterface(new Controls(),"AndroidNode");
        bound=bindService(new Intent(this,NodeService.class),connection,BIND_AUTO_CREATE);
        if(Build.VERSION.SDK_INT>=33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)!=PackageManager.PERMISSION_GRANTED)requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS},2);
    }
    private final class Controls {
        @JavascriptInterface public void startNode(){runOnUiThread(()->startForegroundService(new Intent(MainActivity.this,NodeService.class).setAction(NodeService.START)));}
        @JavascriptInterface public void stopNode(){runOnUiThread(()->startService(new Intent(MainActivity.this,NodeService.class).setAction(NodeService.STOP)));}
        @JavascriptInterface public void copyRequest(String request){if(request==null || request.length()>8192)return;runOnUiThread(()->{((ClipboardManager)getSystemService(CLIPBOARD_SERVICE)).setPrimaryClip(ClipData.newPlainText("Ordexus 钱包操作请求",request));Toast.makeText(MainActivity.this,"请求已复制",Toast.LENGTH_SHORT).show();});}
        @JavascriptInterface public void saveQR(String image){
            try{
                if(image==null || !image.startsWith("data:image/png;base64,") || image.length()>1500000)return;
                byte[] bytes=Base64.decode(image.substring(22),Base64.DEFAULT);
                if(bytes.length<8 || bytes[0]!=(byte)0x89 || bytes[1]!=0x50 || bytes[2]!=0x4e || bytes[3]!=0x47)return;
                if(Build.VERSION.SDK_INT>=29){files.execute(()->saveImage(bytes));}else{pendingPNG=bytes;runOnUiThread(()->startActivityForResult(new Intent(Intent.ACTION_CREATE_DOCUMENT).setType("image/png").addCategory(Intent.CATEGORY_OPENABLE).putExtra(Intent.EXTRA_TITLE,"ordexus-wallet-request.png"),3));}
            }catch(Exception error){runOnUiThread(()->Toast.makeText(MainActivity.this,"二维码保存失败",Toast.LENGTH_LONG).show());}
        }
    }
    private void saveImage(byte[] data){
        Uri image=null;
        try{
            ContentValues values=new ContentValues();values.put(MediaStore.Images.Media.DISPLAY_NAME,"ordexus-wallet-request-"+System.currentTimeMillis()+".png");values.put(MediaStore.Images.Media.MIME_TYPE,"image/png");values.put(MediaStore.Images.Media.RELATIVE_PATH,"Pictures/Ordexus");values.put(MediaStore.Images.Media.IS_PENDING,1);
            image=getContentResolver().insert(MediaStore.Images.Media.EXTERNAL_CONTENT_URI,values);
            if(image==null)throw new IOException("No gallery entry");
            try(OutputStream output=getContentResolver().openOutputStream(image)){if(output==null)throw new IOException("No output stream");output.write(data);}
            values.clear();values.put(MediaStore.Images.Media.IS_PENDING,0);getContentResolver().update(image,values,null,null);
            runOnUiThread(()->Toast.makeText(this,"二维码已保存到相册，可用钱包从相册识别",Toast.LENGTH_LONG).show());
        }catch(Exception error){if(image!=null)getContentResolver().delete(image,null,null);runOnUiThread(()->Toast.makeText(this,"二维码保存失败",Toast.LENGTH_LONG).show());}
    }
    @Override protected void onActivityResult(int request,int result,Intent intent){super.onActivityResult(request,result,intent);if(request==3 && result==RESULT_OK && intent!=null && pendingPNG!=null){byte[] data=pendingPNG;Uri uri=intent.getData();files.execute(()->{try(OutputStream output=getContentResolver().openOutputStream(uri)){if(output!=null)output.write(data);}catch(Exception error){runOnUiThread(()->Toast.makeText(this,"二维码保存失败",Toast.LENGTH_LONG).show());}});}pendingPNG=null;}
    @Override protected void onDestroy(){if(bound){unbindService(connection);bound=false;}panel.removeJavascriptInterface("AndroidNode");panel.destroy();files.shutdown();super.onDestroy();}
}
