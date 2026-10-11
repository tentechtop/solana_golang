package network.ordexus.node;

import android.app.*;
import android.content.*;
import android.os.*;
import nodemobile.App;
import nodemobile.Nodemobile;
import java.util.concurrent.*;

public final class NodeService extends Service {
    public static final String START="network.ordexus.node.START", STOP="network.ordexus.node.STOP";
    public static final int PANEL=1;
    private App node;
    private String panelURL, startupError;
    private PowerManager.WakeLock wakeLock;
    private final ExecutorService operations=Executors.newSingleThreadExecutor();
    private final Handler main=new Handler(Looper.getMainLooper());
    private final Messenger messenger=new Messenger(new Handler(Looper.getMainLooper()) {
        @Override public void handleMessage(Message request) {
            if(request.what!=PANEL || request.replyTo==null)return;
            Message response=Message.obtain(null,PANEL);Bundle data=new Bundle();
            data.putString("url",panelURL);data.putString("error",startupError);response.setData(data);
            try{request.replyTo.send(response);}catch(RemoteException ignored){}
        }
    });
    @Override public void onCreate(){
        super.onCreate();
        NotificationChannel channel=new NotificationChannel("node", "Ordexus 节点运行",NotificationManager.IMPORTANCE_LOW);
        getSystemService(NotificationManager.class).createNotificationChannel(channel);
        wakeLock=((PowerManager)getSystemService(POWER_SERVICE)).newWakeLock(PowerManager.PARTIAL_WAKE_LOCK,"OrdexusNode:engine");wakeLock.setReferenceCounted(false);
        try{node=Nodemobile.newApp(getFilesDir().getAbsolutePath()+"/ordexus-node");panelURL=node.panelURL();}catch(Exception error){startupError=error.getMessage();}
    }
    @Override public IBinder onBind(Intent intent){return messenger.getBinder();}
    @Override public int onStartCommand(Intent intent,int flags,int startId){
        if(intent==null)return START_NOT_STICKY;
        if(START.equals(intent.getAction())){
            startForeground(10,notification("正在启动本机节点"));
            if(!wakeLock.isHeld())wakeLock.acquire();
            operations.execute(()->{try{if(node==null)throw new IllegalStateException(startupError);node.start();}catch(Exception error){main.post(()->android.widget.Toast.makeText(this,error.getMessage(),android.widget.Toast.LENGTH_LONG).show());}main.post(this::checkRunning);});
        }else if(STOP.equals(intent.getAction())){
            operations.execute(()->{try{if(node!=null)node.stop();}catch(Exception error){main.post(()->android.widget.Toast.makeText(this,error.getMessage(),android.widget.Toast.LENGTH_LONG).show());}main.post(this::checkRunning);});
        }
        return START_NOT_STICKY;
    }
    private Notification notification(String message){
        PendingIntent open=PendingIntent.getActivity(this,0,new Intent(this,MainActivity.class),PendingIntent.FLAG_UPDATE_CURRENT|PendingIntent.FLAG_IMMUTABLE);
        PendingIntent stop=PendingIntent.getService(this,1,new Intent(this,NodeService.class).setAction(STOP),PendingIntent.FLAG_UPDATE_CURRENT|PendingIntent.FLAG_IMMUTABLE);
        return new Notification.Builder(this,"node").setSmallIcon(R.drawable.ic_node).setContentTitle("Ordexus Node").setContentText(message).setContentIntent(open).setOngoing(true).addAction(new Notification.Action.Builder(null,"停止节点",stop).build()).build();
    }
    private void checkRunning(){
        main.removeCallbacks(checkTask);
        if(node!=null && node.running()){
            getSystemService(NotificationManager.class).notify(10,notification("本机节点正在运行 · 点击查看管理面板"));
            main.postDelayed(checkTask,10000);
        }else{
            stopForeground(STOP_FOREGROUND_REMOVE);if(wakeLock.isHeld())wakeLock.release();stopSelf();
        }
    }
    private final Runnable checkTask=this::checkRunning;
    @Override public void onDestroy(){
        main.removeCallbacksAndMessages(null);
        if(wakeLock!=null && wakeLock.isHeld())wakeLock.release();
        operations.execute(()->{try{if(node!=null)node.close();}catch(Exception ignored){}});operations.shutdown();
        super.onDestroy();
    }
}
